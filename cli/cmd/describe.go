// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/workload"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

const (
	flagPodLimit = "pod-limit"

	usagePodLimit = "Maximum pod rows per component; the default shows every pod. " +
		"When set, unhealthy pods are shown first. Table output only"

	describeUse   = "describe TYPE[/NAME] [NAME]"
	describeShort = "Show one workload in full"

	describeLong = `Show one workload as Karta reads it through the definition covering its type:
the component tree with live pods attributed to the role they play, the
normalized phase, and the requested resources per component.

Type matching is lenient: case-insensitive, singular or plural, and kubectl short
names all resolve.

Pods are attributed by ownership, so only the pods of this workload are shown, and
by the definition's own pod selectors, so a pod lands under the component whose
role it plays rather than under the object that happens to own it.`

	describeExample = `  # Describe a workload, kubectl-style
  kli describe pytorchjob/llama-finetune

  # Two-token form, also kubectl-style
  kli describe pytorchjob llama-finetune

  # Large workloads: cap the pod rows, unhealthy pods first
  kli describe pytorchjob/llama-finetune --pod-limit 10

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
		Args: usageArgs(cobra.MatchAll(
			cobra.RangeArgs(1, 2),
			func(_ *cobra.Command, args []string) error {
				if err := parseArgs(&opts.getOptions, args); err != nil {
					return err
				}
				if opts.name == "" {
					return errNameRequired
				}
				return nil
			},
		)),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			// A negative limit collides with the ShowAllPods sentinel.
			if cmd.Flags().Changed(flagPodLimit) && opts.podLimit < 0 {
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

	return cmd
}

func runDescribe(cmd *cobra.Command, opts *describeOptions, format generator.Output) error {
	ctx := cmd.Context()

	look, err := resolveLookup(cmd, &opts.getOptions)
	if err != nil {
		return err
	}

	obj, err := getOne(ctx, look.dyn, look.mapper, look.definition, look.namespace, opts.name)
	if err != nil {
		return err
	}

	// Pods are created beside the workload, so the list stays in its namespace.
	// A cluster-scoped root has none, so there it is cluster-wide.
	pods, err := workload.ListPods(ctx, look.dyn, obj.GetNamespace())
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	owned, err := workload.NewPodAttributor(look.dyn, look.mapper).Filter(ctx, pods, obj.GetUID())
	if err != nil {
		return fmt.Errorf("attribute pods: %w", err)
	}

	view, err := workload.ResolveDescribe(ctx, obj, look.definition, owned)
	if err != nil {
		return fmt.Errorf("describe %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}

	return generator.RenderWorkload(cmd.OutOrStdout(), view, generator.DescribeOptions{
		Output:   format,
		PodLimit: opts.podLimit,
	})
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
