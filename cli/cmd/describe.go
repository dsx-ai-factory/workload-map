// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"context"
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
		"When set, unhealthy pods are shown first"

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

// describeOptions holds one run's inputs. Embedding getOptions is what makes
// describe accept the same TYPE/NAME forms as get.
type describeOptions struct {
	getOptions
	podLimit int
}

// newDescribeCommand builds the "kli describe" command: one workload in full.
func newDescribeCommand() *cobra.Command {
	opts := &describeOptions{podLimit: generator.ShowAllPods}
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
					return fmt.Errorf(
						"a NAME is required: give it as %s or as %s", "TYPE/NAME", "TYPE NAME")
				}
				return nil
			},
		)),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDescribe(cmd, opts, output.Get())
		},
	}

	// A single workload renders no extra columns, so wide is rejected at parse
	// time rather than silently treated as the table.
	output = withOutput(cmd, cmd.Flags(), false)
	cmd.Flags().IntVar(&opts.podLimit, flagPodLimit, generator.ShowAllPods, usagePodLimit)

	return cmd
}

func runDescribe(cmd *cobra.Command, opts *describeOptions, format generator.Output) error {
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

	mapper, err := access.ToRESTMapper()
	if err != nil {
		return fmt.Errorf("kubernetes discovery: %w", err)
	}
	dyn, err := newDynamicClient(access)
	if err != nil {
		return err
	}

	target, err := resolveTarget(&opts.getOptions, resolver, mapper)
	if err != nil {
		return err
	}

	obj, err := getOne(ctx, dyn, mapper, target, namespace, opts.name)
	if err != nil {
		return err
	}

	// Pods are read from the object's own namespace: a workload's pods are
	// created beside it, and the request must not widen when -n was omitted.
	pods, err := workload.ListPods(ctx, dyn, obj.GetNamespace())
	if err != nil {
		return fmt.Errorf("list pods: %w", err)
	}
	mine, err := workload.NewPodAttributor(dyn, mapper).Filter(ctx, pods, obj.GetUID())
	if err != nil {
		return fmt.Errorf("attribute pods: %w", err)
	}

	view, err := workload.ResolveDescribe(ctx, obj, target, mine)
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
		return nil, exitError{code: ExitError,
			err: fmt.Errorf("not allowed to read %s: %w", gvk.Kind, err)}
	default:
		return nil, exitError{code: ExitError,
			err: fmt.Errorf("get %s %q: %w", gvk.Kind, name, err)}
	}
}
