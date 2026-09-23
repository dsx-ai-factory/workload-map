// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

const (
	flagPodLimit = "pod-limit"
	flagFile     = "file"

	usagePodLimit = "Maximum pod rows per component; the default shows every pod. " +
		"When set, unhealthy pods are shown first. Table output only"
	usageFile = "Describe a manifest that has not been submitted; \"-\" reads stdin. " +
		"No cluster is needed, and no TYPE/NAME is accepted"

	describeUse   = "describe TYPE[/NAME] [NAME]"
	describeShort = "Show one workload in full"

	describeLong = `Show one workload as Karta reads it through the definition covering its type:
the component tree with live pods attributed to the role they play, the
normalized phase, and the requested resources per component.

Type matching is lenient: case-insensitive, singular or plural, and kubectl short
names all resolve.

Pods are attributed by ownership, so only the pods of this workload are shown, and
by the definition's own pod selectors, so a pod lands under the component whose
role it plays rather than under the object that happens to own it.

With -f, the same view is built from a manifest alone, with no cluster and no
pods: the structure and the desired scale are real, everything live is absent.
It answers what a workload would look like before it is submitted.`

	describeExample = `  # Describe a workload, kubectl-style
  kli describe pytorchjob/llama-finetune

  # Two-token form, also kubectl-style
  kli describe pytorchjob llama-finetune

  # Large workloads: cap the pod rows, unhealthy pods first
  kli describe pytorchjob/llama-finetune --pod-limit 10

  # Validate a manifest before submitting it, no cluster needed
  kli describe -f jobset.yaml

  # Machine output for scripting or agents
  kli describe pytorchjob/llama-finetune -o json`
)

// errNameRequired names both accepted forms, so a reader sees the one they did
// not use rather than only the one they did.
var errNameRequired = errors.New("a NAME is required: give it as TYPE/NAME or as TYPE NAME")

// errNoDefinitions separates "nothing loaded at all" from a type no definition
// covers, which sends the reader somewhere else entirely.
var errNoDefinitions = errors.New("no Karta definitions available (catalog empty and no cluster definitions)")

// describeOptions holds one run's inputs. Embedding getOptions is what makes
// describe accept the same TYPE/NAME forms as get.
type describeOptions struct {
	getOptions
	podLimit int
	file     string
}

// newDescribeCommand builds the "kli describe" command: one workload in full.
func newDescribeCommand() *cobra.Command {
	opts := &describeOptions{}
	var output *Enum[generator.Output]

	cmd := &cobra.Command{
		Use:     describeUse,
		Short:   describeShort,
		Long:    describeLong,
		Example: describeExample,
		// The range applies to the positional form only: -f reads the kind from
		// the manifest, so it takes no arguments at all.
		Args: usageArgs(func(cmd *cobra.Command, args []string) error {
			if opts.file != "" {
				if len(args) > 0 {
					return fmt.Errorf(
						"--%s reads the kind from the manifest, so it cannot be combined with %q",
						flagFile, strings.Join(args, " "))
				}
				return nil
			}
			if err := cobra.RangeArgs(1, 2)(cmd, args); err != nil {
				return err
			}
			if err := parseArgs(&opts.getOptions, args); err != nil {
				return err
			}
			if opts.name == "" {
				return errNameRequired
			}
			return nil
		}),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			// A negative limit collides with the ShowAllPods sentinel.
			if opts.podLimit < 0 {
				return usageError(cmd, fmt.Errorf("--%s must not be negative", flagPodLimit))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDescribe(cmd, opts, output.Get())
		},
	}

	// A single workload renders no extra columns, so wide is rejected at parse
	// time rather than silently treated as the table.
	output = withOutput(cmd, cmd.Flags(), false)
	// Zero is the default rather than ShowAllPods: both mean no limit, and only
	// zero keeps pflag from advertising a value the flag then rejects.
	cmd.Flags().IntVar(&opts.podLimit, flagPodLimit, 0, usagePodLimit)
	cmd.Flags().StringVarP(&opts.file, flagFile, "f", "", usageFile)

	return cmd
}

func runDescribe(cmd *cobra.Command, opts *describeOptions, format generator.Output) error {
	ctx := cmd.Context()

	view, err := resolveView(ctx, cmd, opts)
	if err != nil {
		return reportNoDefinition(cmd, format, err)
	}

	return generator.RenderWorkload(cmd.OutOrStdout(), view, generator.DescribeOptions{
		Output:   format,
		PodLimit: opts.podLimit,
	})
}

func resolveView(
	ctx context.Context, cmd *cobra.Command, opts *describeOptions,
) (*workload.DescribeView, error) {
	if opts.file != "" {
		return describeManifest(ctx, cmd, opts)
	}
	return describeLive(ctx, cmd, opts)
}

// describeLive reads the named workload and its pods from the cluster.
func describeLive(
	ctx context.Context, cmd *cobra.Command, opts *describeOptions,
) (*workload.DescribeView, error) {
	look, err := resolveLookup(cmd, &opts.getOptions)
	if err != nil {
		return nil, err
	}

	obj, err := getOne(ctx, look.dyn, look.mapper, look.definition, look.namespace, opts.name)
	if err != nil {
		return nil, err
	}

	// Pods are created beside the workload, so the list stays in its namespace.
	// A cluster-scoped root has none, so there it is cluster-wide.
	pods, err := workload.ListPods(ctx, look.dyn, obj.GetNamespace())
	if err != nil {
		return nil, fmt.Errorf("list pods: %w", err)
	}
	owned, err := workload.NewPodAttributor(look.dyn, look.mapper).Filter(ctx, pods, obj.GetUID())
	if err != nil {
		return nil, fmt.Errorf("attribute pods: %w", err)
	}

	view, err := workload.ResolveDescribe(ctx, obj, look.definition, owned)
	if err != nil {
		return nil, fmt.Errorf("describe %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}
	return view, nil
}

// describeManifest builds the view from a manifest alone. The kind comes from
// the manifest, so no discovery is involved and no cluster is required.
func describeManifest(
	ctx context.Context, cmd *cobra.Command, opts *describeOptions,
) (*workload.DescribeView, error) {
	obj, err := readManifest(cmd, opts.file)
	if err != nil {
		return nil, err
	}

	// Read best-effort: an unreachable cluster degrades to the embedded catalog,
	// which is what lets file mode work with no cluster at all.
	resolver, warnings := loadDefinitions(ctx, clusterAccess())
	if err := printWarnings(cmd.ErrOrStderr(), warningMessages(warnings)); err != nil {
		return nil, err
	}

	gvk := obj.GroupVersionKind()
	if gvk.Kind == "" || gvk.Version == "" {
		return nil, usageError(cmd, fmt.Errorf(
			"%s declares no apiVersion and kind, so there is nothing to resolve it by", opts.file))
	}

	target, err := resolver.Resolve(gvk)
	switch {
	case err == nil:
	case errors.Is(err, definitions.ErrAmbiguous):
		return nil, exitError{code: ExitUsage, err: err}
	default:
		// A CRD serves several versions, and a definition covers the kind at
		// one of them, so a manifest written at another still resolves.
		var matches []definitions.Definition
		for _, match := range resolver.ByRootKind(gvk.Kind) {
			if strings.EqualFold(catalog.RootKey(match.Karta).Group, gvk.Group) {
				matches = append(matches, match)
			}
		}
		switch len(matches) {
		case 0:
			return nil, noDefinitionFor(gvk)
		case 1:
			target = matches[0]
		default:
			return nil, exitError{code: ExitUsage, err: ambiguous(gvk.GroupKind().String(), matches)}
		}
	}

	// No pods: a manifest that never reached the cluster has none, and inventing
	// zeroes would read as a workload whose pods have all gone.
	view, err := workload.ResolveDescribe(ctx, obj, target, nil)
	if err != nil {
		return nil, fmt.Errorf("describe %s: %w", opts.file, err)
	}
	view.FileMode = true
	return view, nil
}

// readManifest decodes one workload manifest, from stdin when path is "-".
func readManifest(cmd *cobra.Command, path string) (*unstructured.Unstructured, error) {
	var (
		raw []byte
		err error
	)
	if path == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Unmarshal reads only the first document, so a stream is split first: a
	// leading ConfigMap would otherwise be described in place of the workload.
	var fields map[string]any
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(raw)))
	for {
		doc, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, usageError(cmd, fmt.Errorf("parse %s: %w", path, err))
		}

		// A plain map, not Unstructured: its unmarshal rejects a missing kind
		// with a message about JSON internals rather than the field that is absent.
		var next map[string]any
		if err := yaml.Unmarshal(doc, &next); err != nil {
			return nil, usageError(cmd, fmt.Errorf("parse %s: %w", path, err))
		}
		switch {
		case len(next) == 0:
			// An empty or comment-only document separates, it describes nothing.
		case fields != nil:
			return nil, usageError(cmd, fmt.Errorf(
				"%s holds more than one document; describe reads one workload", path))
		default:
			fields = next
		}
	}
	return &unstructured.Unstructured{Object: fields}, nil
}

// machineError is the shape a failure takes in the machine formats. Only the
// no-definition case uses it, being the one an agent handles differently.
type machineError struct {
	Error   string `json:"error"`
	GVK     string `json:"gvk,omitempty"`
	Type    string `json:"type,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

const (
	noDefinitionReason  = "no_definition_for_type"
	noDefinitionMessage = "no Karta definition covers this workload type"
	noDefinitionHint    = `"kli definitions" lists the types Karta covers; ` +
		"apply a Karta definition to the cluster to cover this one"
)

// noDefinitionNotFound marks a failure reportNoDefinition should also emit as a
// machine error, carrying the identity the payload names.
type noDefinitionNotFound struct {
	exitError
	subject machineError
}

// noDefinitionForType is the miss for a type token that matched no definition.
// Without discovery there is no GVK, so the payload names the token instead.
func noDefinitionForType(token string) error {
	return noDefinitionNotFound{
		exitError: exitError{code: ExitNotFound,
			err: fmt.Errorf("no Karta definition covers %q", token)},
		subject: machineError{
			Error: noDefinitionReason, Type: token,
			Message: noDefinitionMessage, Hint: noDefinitionHint,
		},
	}
}

func noDefinitionFor(gvk schema.GroupVersionKind) error {
	return noDefinitionNotFound{
		exitError: exitError{code: ExitNotFound,
			err: fmt.Errorf("%s: %s", noDefinitionMessage, gvk)},
		subject: machineError{
			Error: noDefinitionReason, GVK: gvk.String(),
			Message: noDefinitionMessage, Hint: noDefinitionHint,
		},
	}
}

// reportNoDefinition puts the machine error on stdout, where an agent reads,
// and leaves the human message on stderr, where a reader does.
func reportNoDefinition(cmd *cobra.Command, format generator.Output, err error) error {
	if format != generator.OutputJSON && format != generator.OutputYAML {
		return err
	}

	// An empty definition set shares the exit code but not the payload: it
	// says nothing about this type, and the payload would claim it does.
	var structured noDefinitionNotFound
	if !errors.As(err, &structured) {
		return err
	}

	if writeErr := generator.RenderOne(cmd.OutOrStdout(), format, structured.subject,
		func(io.Writer) error { return nil }); writeErr != nil {
		return writeErr
	}
	return err
}

// getOne fetches the single named object of target's type, reporting a miss the
// way get reports one so a script sees the same code either way.
func getOne(
	ctx context.Context,
	dyn dynamic.Interface,
	mapper meta.RESTMapper,
	target definitions.Definition,
	namespace, name string,
) (*unstructured.Unstructured, error) {
	gvk := catalog.RootKey(target.Karta)

	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	switch {
	case err == nil:
	case meta.IsNoMatchError(err):
		// The type is absent, so the named workload cannot exist.
		return nil, exitError{code: ExitWorkloadNotFound,
			err: fmt.Errorf("%s is not installed in this cluster", gvk.Kind)}
	default:
		return nil, fmt.Errorf("discover %s: %w", gvk.Kind, err)
	}

	// A cluster-scoped root is not addressed by namespace.
	if mapping.Scope.Name() == meta.RESTScopeNameRoot {
		namespace = ""
	}

	obj, err := dyn.Resource(mapping.Resource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	switch {
	case err == nil:
		return obj, nil
	case apierrors.IsNotFound(err):
		return nil, exitError{code: ExitWorkloadNotFound,
			err: fmt.Errorf("%s %q not found%s", gvk.Kind, name, inNamespace(namespace))}
	case apierrors.IsForbidden(err):
		return nil, fmt.Errorf("not allowed to read %s: %w", gvk.Kind, err)
	default:
		return nil, fmt.Errorf("get %s %q: %w", gvk.Kind, name, err)
	}
}
