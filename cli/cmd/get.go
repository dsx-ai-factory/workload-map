// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

const (
	flagPhase     = "phase"
	flagSelector  = "selector"
	flagChunkSize = "chunk-size"

	usagePhase     = "Filter by normalized phase; repeatable, and applied after resolution so it does not reduce API cost. One of "
	usageSelector  = "Label selector on the workload root, kubectl syntax"
	usageChunkSize = "API list page size; 0 lists without paging. Bounds per-request pressure, not total memory or time to first row"

	// defaultChunkSize follows the kubectl convention for list page size.
	defaultChunkSize = 500

	getUse   = "get TYPE[/NAME] [NAME]"
	getShort = "List workloads of a type"

	getLong = `List workloads with a normalized phase, read through the Karta definition that
covers each type.

Type matching is lenient: case-insensitive, singular or plural, and kubectl short names
all resolve.

The phase comes from the workload spec, so no pods are listed. -o wide adds the ORIGIN
of the resolving definition; the component breakdown, requested GPUs and a NODES column
arrive with the describe command.`

	getExample = `  # All JobSets in the current namespace
  kli get jobset

  # Failed JobSets
  kli get jobset --phase Failed

  # Degraded or failed JobSets matching a selector, as JSON
  kli get jobset --phase Degraded --phase Failed -l team=nlp -o json`
)

// loadDefinitions is a variable so tests can supply a definition set.
var loadDefinitions = definitions.Load

// newDynamicClient is a variable so tests can inject a fake cluster.
var newDynamicClient = func(rcg genericclioptions.RESTClientGetter) (dynamic.Interface, error) {
	cfg, err := RESTConfig(rcg)
	if err != nil {
		return nil, err
	}
	// Set per-config: a global handler would land in machine-readable stderr.
	cfg.WarningHandler = rest.NoWarnings{}

	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes dynamic client: %w", err)
	}
	return client, nil
}

// getOptions holds the resolved inputs for one run of the get command.
type getOptions struct {
	typeToken string
	name      string
	phases    []string
	selector  string
	chunkSize int64
}

// newGetCommand builds the "kli get" command: one row per workload root.
func newGetCommand() *cobra.Command {
	opts := &getOptions{}
	var (
		output *Enum[generator.Output]
		phase  *EnumSlice[string]
	)

	cmd := &cobra.Command{
		Use:     getUse,
		Short:   getShort,
		Long:    getLong,
		Example: getExample,
		Args: usageArgs(cobra.MatchAll(
			cobra.RangeArgs(1, 2),
			func(_ *cobra.Command, args []string) error { return parseArgs(opts, args) },
		)),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			opts.phases = phase.Get()
			return validateOptions(cmd, opts)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runGet(cmd, opts, output.Get())
		},
	}

	output = withOutput(cmd, cmd.Flags(), true)
	phase = withPhase(cmd, cmd.Flags())
	cmd.Flags().StringVarP(&opts.selector, flagSelector, "l", "", usageSelector)
	cmd.Flags().Int64Var(&opts.chunkSize, flagChunkSize, defaultChunkSize, usageChunkSize)

	return cmd
}

// validateOptions rejects the flag values and combinations the command cannot act on.
func validateOptions(cmd *cobra.Command, opts *getOptions) error {
	if opts.chunkSize < 0 {
		return usageError(cmd, fmt.Errorf("--%s must not be negative", flagChunkSize))
	}
	if _, err := labels.Parse(opts.selector); err != nil {
		return usageError(cmd, fmt.Errorf("--%s: %w", flagSelector, err))
	}
	if opts.selector != "" && opts.name != "" {
		return usageError(cmd, fmt.Errorf("--%s cannot be combined with a NAME", flagSelector))
	}
	return nil
}

// parseArgs accepts the three kubectl argument forms: TYPE, TYPE/NAME, and
// TYPE NAME. The count is already bounded by cobra.RangeArgs, so only the shape
// of the arguments is checked here.
func parseArgs(opts *getOptions, args []string) error {
	var qualified bool
	opts.typeToken, opts.name, qualified = strings.Cut(args[0], "/")
	if qualified && opts.name == "" {
		return fmt.Errorf("NAME is empty in %q", args[0])
	}
	if len(args) == 2 {
		if opts.name != "" {
			return fmt.Errorf("NAME given twice: as %q and as %q", args[0], args[1])
		}
		if args[1] == "" {
			return fmt.Errorf("NAME is empty")
		}
		opts.name = args[1]
	}
	if strings.Contains(opts.name, "/") {
		return fmt.Errorf("NAME must not contain %q: %q", "/", opts.name)
	}
	if opts.typeToken == "" {
		return fmt.Errorf("TYPE is empty in %q", args[0])
	}
	return nil
}

func runGet(cmd *cobra.Command, opts *getOptions, format generator.Output) error {
	ctx := cmd.Context()
	access := clusterAccess()

	namespace, _, err := ResolvedNamespace(access)
	if err != nil {
		return fmt.Errorf("resolve namespace: %w", err)
	}

	resolver, warnings := loadDefinitions(ctx, access)
	if err := printWarnings(cmd.ErrOrStderr(), warningMessages(warnings)); err != nil {
		return err
	}
	if len(resolver.List()) == 0 {
		return exitError{code: ExitNotFound, err: fmt.Errorf(
			"no Karta definitions available (catalog empty and no cluster definitions)")}
	}

	mapper, err := access.ToRESTMapper()
	if err != nil {
		return fmt.Errorf("kubernetes discovery: %w", err)
	}
	dyn, err := newDynamicClient(access)
	if err != nil {
		return err
	}

	target, err := resolveTarget(opts, resolver, mapper)
	if err != nil {
		return err
	}

	views, searched, listWarnings, err := collect(ctx, dyn, mapper, target, namespace, opts)
	if writeErr := printWarnings(cmd.ErrOrStderr(), listWarnings); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return err
	}

	// Newest first, so a freshly submitted workload is at the top.
	slices.SortStableFunc(views, func(a, b workload.View) int {
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return b.CreatedAt.Compare(a.CreatedAt)
		}
		return strings.Compare(a.Name, b.Name)
	})

	return generator.RenderWorkloads(cmd.OutOrStdout(), cmd.ErrOrStderr(), views, generator.Options{
		Output:    format,
		Namespace: searched,
		// An empty namespace means the type is cluster-scoped, so the search
		// spanned the cluster.
		AllNamespaces: searched == "",
	})
}

// resolveTarget maps the requested type to the definition that covers it.
func resolveTarget(
	opts *getOptions,
	resolver *definitions.Resolver,
	mapper meta.RESTMapper,
) (definitions.Definition, error) {
	// Discovery resolves short names, plurals and qualified forms. A dotted
	// token has two readings, and ParseResourceArg leaves the choice to callers.
	qualified, groupResource := schema.ParseResourceArg(strings.ToLower(opts.typeToken))
	candidates := []schema.GroupVersionResource{groupResource.WithVersion("")}
	if qualified != nil {
		candidates = append([]schema.GroupVersionResource{*qualified}, candidates...)
	}
	for _, candidate := range candidates {
		def, ok, err := resolveVia(mapper, resolver, candidate)
		if err != nil {
			return definitions.Definition{}, err
		}
		if ok {
			return def, nil
		}
	}

	// Without discovery only an exact Kind matches, but a type whose CRD is
	// absent must still resolve, including the qualified form suggested below.
	switch matches := matchDefinitions(resolver, opts.typeToken); len(matches) {
	case 0:
		return definitions.Definition{}, exitError{code: ExitNotFound,
			err: fmt.Errorf("no Karta definition covers %q", opts.typeToken)}
	case 1:
		return matches[0], nil
	default:
		return definitions.Definition{}, exitError{code: ExitUsage,
			err: ambiguous(opts.typeToken, matches)}
	}
}

// ambiguous explains a type token several definitions claim. Identical root
// GVKs cannot be retried with a qualified token, so those name the definitions.
func ambiguous(token string, matches []definitions.Definition) error {
	tokens := make([]string, 0, len(matches))
	for _, match := range matches {
		tokens = append(tokens, qualifiedToken(catalog.RootKey(match.Karta)))
	}
	slices.Sort(tokens)
	if distinct := slices.Compact(slices.Clone(tokens)); len(distinct) > 1 {
		return fmt.Errorf("%q matches more than one Karta definition, retry with one of: %s",
			token, strings.Join(distinct, ", "))
	}

	names := make([]string, 0, len(matches))
	for _, match := range matches {
		names = append(names, fmt.Sprintf("%q", match.Karta.Name))
	}
	slices.Sort(names)
	return fmt.Errorf("%d Karta definitions claim %s: %s; remove one from the cluster",
		len(names), tokens[0], strings.Join(names, ", "))
}

// qualifiedToken renders the kind.version.group form that names one definition
// unambiguously.
func qualifiedToken(gvk schema.GroupVersionKind) string {
	return fmt.Sprintf("%s.%s.%s", strings.ToLower(gvk.Kind), gvk.Version, gvk.Group)
}

// matchDefinitions matches a type token against the definitions themselves,
// narrowing by group and version when the token carries them.
func matchDefinitions(resolver *definitions.Resolver, token string) []definitions.Definition {
	qualified, groupKind := schema.ParseKindArg(token)

	matches := resolver.ByRootKind(groupKind.Kind)
	if groupKind.Group == "" {
		return matches
	}

	narrowed := make([]definitions.Definition, 0, len(matches))
	for _, match := range matches {
		switch gvk := catalog.RootKey(match.Karta); {
		case qualified != nil && strings.EqualFold(gvk.Group, qualified.Group) &&
			strings.EqualFold(gvk.Version, qualified.Version):
			narrowed = append(narrowed, match)
		case strings.EqualFold(gvk.Group, groupKind.Group):
			narrowed = append(narrowed, match)
		}
	}
	return narrowed
}

// resolveVia turns a partial resource into a kind and looks it up. A miss lets
// the caller try the next reading; a collision must not fall through.
func resolveVia(
	mapper meta.RESTMapper,
	resolver *definitions.Resolver,
	gvr schema.GroupVersionResource,
) (definitions.Definition, bool, error) {
	gvk, err := mapper.KindFor(gvr)
	switch {
	case err == nil:
	case meta.IsNoMatchError(err), meta.IsAmbiguousError(err):
		return definitions.Definition{}, false, nil
	default:
		// Discovery failing is not the same as the type being unknown, and
		// reporting it as unknown would depend on how the type was spelled.
		return definitions.Definition{}, false, fmt.Errorf("discover %q: %w", gvr.Resource, err)
	}

	def, err := resolver.Resolve(gvk)
	switch {
	case err == nil:
		return def, true, nil
	case errors.Is(err, definitions.ErrAmbiguous):
		return definitions.Definition{}, false, exitError{code: ExitUsage, err: err}
	default:
		return definitions.Definition{}, false, nil
	}
}

// collect lists the target type and resolves every object into a view. A single
// object that fails to resolve degrades to a warning; the listing itself does not.
func collect(
	ctx context.Context,
	dyn dynamic.Interface,
	mapper meta.RESTMapper,
	def definitions.Definition,
	namespace string,
	opts *getOptions,
) (views []workload.View, searched string, warnings []string, err error) {
	gvk := catalog.RootKey(def.Karta)

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	switch {
	case meta.IsNoMatchError(err) && opts.name != "":
		// The type is absent, so the named workload cannot exist: the caller
		// needs the same "no such workload" fallback as a plain miss.
		return nil, namespace, nil, exitError{code: ExitWorkloadNotFound,
			err: fmt.Errorf("%s is not installed in this cluster", gvk.Kind)}
	case meta.IsNoMatchError(err):
		// No object of this type can exist, so an empty result is the answer.
		return nil, namespace,
			[]string{fmt.Sprintf("%s is not installed in this cluster", gvk.Kind)}, nil
	case err != nil:
		// Discovery failed, so reporting this type as "not installed" would be
		// misleading.
		return nil, namespace, nil, fmt.Errorf("discover %s: %w", gvk.Kind, err)
	}

	// A cluster-scoped root is searched cluster-wide, so the namespace must drop
	// out of the diagnostics too.
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		namespace = ""
	}

	objects, err := list(ctx, dyn, mapping, namespace, opts)
	switch {
	case err == nil:
	case apierrors.IsNotFound(err) && opts.name != "":
		return nil, namespace, nil, exitError{code: ExitWorkloadNotFound,
			err: fmt.Errorf("%s %q not found%s", gvk.Kind, opts.name, inNamespace(namespace))}
	case opts.name != "":
		return nil, namespace, nil, exitError{code: ExitError,
			err: fmt.Errorf("get %s %q: %w", gvk.Kind, opts.name, err)}
	case apierrors.IsForbidden(err):
		// Being denied is not an empty result: the caller cannot be told either
		// way, so it must not read as "no workloads found".
		return nil, namespace, nil, exitError{code: ExitError,
			err: fmt.Errorf("not allowed to list %s: %w", gvk.Kind, err)}
	default:
		return nil, namespace, nil, fmt.Errorf("list %s: %w", gvk.Kind, err)
	}

	for i := range objects {
		object := &objects[i]
		view, err := workload.Resolve(ctx, object, def)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not resolve %s/%s: %v",
				gvk.Kind, object.GetName(), err))
			continue
		}
		if !matchesPhase(view, opts.phases) {
			continue
		}
		views = append(views, *view)
	}

	return views, namespace, warnings, nil
}

// list reads one type, following continuation tokens. A named request is a Get,
// so a missing name is NotFound rather than an empty list.
func list(
	ctx context.Context,
	dyn dynamic.Interface,
	mapping *meta.RESTMapping,
	namespace string,
	opts *getOptions,
) ([]unstructured.Unstructured, error) {
	client := dyn.Resource(mapping.Resource).Namespace(namespace)

	if opts.name != "" {
		object, err := client.Get(ctx, opts.name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return []unstructured.Unstructured{*object}, nil
	}

	var objects []unstructured.Unstructured
	options := metav1.ListOptions{Limit: opts.chunkSize, LabelSelector: opts.selector}
	for {
		page, err := client.List(ctx, options)
		if err != nil {
			return nil, err
		}
		objects = append(objects, page.Items...)

		options.Continue = page.GetContinue()
		if options.Continue == "" {
			return objects, nil
		}
	}
}

// inNamespace renders the namespace clause, which a cluster-wide search omits.
func inNamespace(namespace string) string {
	if namespace == "" {
		return ""
	}
	return " in namespace " + namespace
}

func matchesPhase(view *workload.View, requested []string) bool {
	if len(requested) == 0 {
		return true
	}
	// Both sides come from the ResourceStatus enum, so the match is exact.
	for _, want := range requested {
		if slices.Contains(view.Phases, want) {
			return true
		}
	}
	return false
}
