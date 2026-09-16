// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package workload

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
	"github.com/dsx-ai-factory/workload-map/pkg/tree"
)

// gpuResourceName is the extended resource a GPU request is declared under.
const gpuResourceName = corev1.ResourceName("nvidia.com/gpu")

// DescribeView is one workload in full. Every describe rendering draws from this
// one struct, so the human and the machine output cannot drift apart.
type DescribeView struct {
	// View is embedded, so its fields stay flat in JSON and cannot drift from
	// what the get command reports for the same workload.
	View `json:",inline"`
	// FileMode marks a view built from a manifest: the structure and desired
	// scale are real, everything live is absent rather than zero.
	FileMode bool `json:"fileMode"`
	// Resources is the whole workload's request, the sum over Components.
	Resources  Resources       `json:"resources"`
	Components []ComponentView `json:"components"`
}

// ComponentView is one component, or one instance of a multi-instance
// component, with the pods that were attributed to it.
type ComponentView struct {
	Name string `json:"name"`
	// Kind is empty for a logical grouping component, which owns no objects.
	Kind      string    `json:"kind,omitempty"`
	Replicas  Replicas  `json:"replicas"`
	Resources Resources `json:"resources"`
	// Nodes are the distinct nodes this component's pods landed on.
	Nodes    []string        `json:"nodes,omitempty"`
	Pods     []PodView       `json:"pods,omitempty"`
	Children []ComponentView `json:"children,omitempty"`
}

// Replicas counts a component three ways: what the spec asks for, how many
// pods exist, and how many of those are ready.
type Replicas struct {
	Desired int32 `json:"desired"`
	Current int32 `json:"current"`
	Ready   int32 `json:"ready"`
}

func (r Replicas) isEmpty() bool {
	return r == Replicas{}
}

// PodView is one live pod attributed to a component.
type PodView struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Ready bool   `json:"ready"`
	// Node is null for a pod that has not been scheduled.
	Node *string `json:"node"`
	// Reason explains a pod that is not running, e.g. "Unschedulable".
	Reason    string    `json:"reason,omitempty"`
	Resources Resources `json:"resources"`
}

// Resources is a request total. CPU is in millicores and memory in bytes, so
// every field is an integer a consumer can sum without unit handling.
type Resources struct {
	GPUs        int64 `json:"gpus"`
	CPUMillis   int64 `json:"cpuMillis"`
	MemoryBytes int64 `json:"memoryBytes"`
}

func (r *Resources) add(other Resources) {
	r.GPUs += other.GPUs
	r.CPUMillis += other.CPUMillis
	r.MemoryBytes += other.MemoryBytes
}

func (r Resources) isEmpty() bool {
	return r == Resources{}
}

func (r Resources) scaled(replicas int32) Resources {
	return Resources{
		GPUs:        r.GPUs * int64(replicas),
		CPUMillis:   r.CPUMillis * int64(replicas),
		MemoryBytes: r.MemoryBytes * int64(replicas),
	}
}

// ResolveDescribe reads obj through def, placing pods on the component and
// instance its selectors name. pods must already be scoped. Passing none yields
// the same view with every live field empty, which is what file mode renders;
// marking the view FileMode is the caller's, since only it knows the source.
func ResolveDescribe(
	ctx context.Context, obj *unstructured.Unstructured, def definitions.Definition, pods []corev1.Pod,
) (*DescribeView, error) {
	factory := resource.NewComponentFactoryFromObject(def.Karta, obj)

	workloadTree, err := tree.Build(ctx, factory)
	if err != nil {
		return nil, fmt.Errorf("build tree: %w", err)
	}

	view := &DescribeView{
		View:       viewOf(obj, def, workloadTree),
		Components: []ComponentView{},
	}

	defs := indexComponentDefs(def.Karta)

	// tree.Build drops the root, but Deployment, StatefulSet, Job and Pod carry
	// their pod template on it, so the root itself can be pod-bearing.
	root, err := factory.GetRootComponent()
	if err != nil {
		return nil, fmt.Errorf("get root component: %w", err)
	}
	if root.HasPodDefinition() {
		instances, err := rootInstances(ctx, root)
		if err != nil {
			return nil, err
		}
		// The root takes the same pod filter and the same selectorless rule
		// buildComponent applies to a child, so being the root is never on its
		// own a licence to claim a pod another component owns.
		claimed, err := matchComponentType(ctx, defs[root.Name()].PodSelector, pods, isSolePodOwner(defs))
		if err != nil {
			return nil, fmt.Errorf("match pods to root component: %w", err)
		}

		component, err := buildPodBearingComponent(ctx, root.Name(), kindOf(root.Kind()), instances, claimed, defs)
		if err != nil {
			return nil, fmt.Errorf("build root component: %w", err)
		}
		view.Components = appendComponent(view.Components, component, true)
	}

	for _, node := range workloadTree.Children {
		component, err := buildComponent(ctx, node, defs, pods)
		if err != nil {
			return nil, fmt.Errorf("build component %q: %w", node.Name, err)
		}
		view.Components = appendComponent(view.Components, component, node.HasPodDefinition)
	}

	for _, component := range view.Components {
		view.Resources.add(component.Resources)
	}
	return view, nil
}

// rootInstances rebuilds the instance nodes tree.Build discards for the root,
// so a root-hosted pod template goes through the same path as any component.
func rootInstances(ctx context.Context, root *resource.Component) ([]tree.InstanceNode, error) {
	extracted, err := root.GetExtractedInstances(ctx)
	if err != nil {
		return nil, fmt.Errorf("extract root instances: %w", err)
	}

	instances := make([]tree.InstanceNode, 0, len(extracted))
	for _, id := range slices.Sorted(maps.Keys(extracted)) {
		instance := extracted[id]
		node := tree.InstanceNode{Scale: instance.Scale, ExtractedInstance: &instance}
		if id != "" {
			node.InstanceKey = &id
		}
		instances = append(instances, node)
	}
	return instances, nil
}

// isSolePodOwner reports a definition with a single pod-bearing component, the
// one case where a component needs no selector to be sure a pod is its own.
func isSolePodOwner(defs map[string]v1alpha1.ComponentDefinition) bool {
	owners := 0
	for _, def := range defs {
		if def.SpecDefinition != nil {
			owners++
		}
	}
	return owners == 1
}

func indexComponentDefs(karta *v1alpha1.Karta) map[string]v1alpha1.ComponentDefinition {
	defs := make(map[string]v1alpha1.ComponentDefinition, len(karta.Spec.StructureDefinition.ChildComponents)+1)
	defs[karta.Spec.StructureDefinition.RootComponent.Name] = karta.Spec.StructureDefinition.RootComponent
	for _, child := range karta.Spec.StructureDefinition.ChildComponents {
		defs[child.Name] = child
	}
	return defs
}

// buildComponent renders one ComponentNode and the subtree under it, choosing
// between the two kinds of component by whether the node carries pods.
func buildComponent(
	ctx context.Context, node tree.ComponentNode, defs map[string]v1alpha1.ComponentDefinition, pods []corev1.Pod,
) (ComponentView, error) {
	kind := kindOf(node.Kind)
	def := defs[node.Name]

	claimed := pods
	if node.HasPodDefinition {
		var err error
		if claimed, err = matchComponentType(ctx, def.PodSelector, pods, isSolePodOwner(defs)); err != nil {
			return ComponentView{}, fmt.Errorf("match pods to component %q: %w", node.Name, err)
		}
	}

	if !isMultiInstance(node.Instances) {
		if node.HasPodDefinition {
			return buildPodBearingComponent(ctx, node.Name, kind, node.Instances, claimed, defs)
		}
		return buildGroupingComponent(ctx, node.Name, kind, node.Instances, claimed, defs)
	}

	// A grouping component takes this path too: it holds no pods itself, but its
	// ReplicaSelector still decides which pods its descendants may see.
	parent := ComponentView{Name: node.Name, Kind: kind}
	for _, instance := range node.Instances {
		scoped, err := filterByInstance(ctx, def.PodSelector, instance, claimed)
		if err != nil {
			return ComponentView{}, fmt.Errorf("match pods to instance of %q: %w", node.Name, err)
		}

		label := instanceLabel(node.Name, instance)
		one := []tree.InstanceNode{instance}

		var child ComponentView
		if node.HasPodDefinition {
			child, err = buildPodBearingComponent(ctx, label, kind, one, scoped, defs)
		} else {
			child, err = buildGroupingComponent(ctx, label, kind, one, scoped, defs)
		}
		if err != nil {
			return ComponentView{}, err
		}

		parent.Children = appendComponent(parent.Children, child, node.HasPodDefinition)
		parent.aggregate(child)
	}
	return parent, nil
}

// buildGroupingComponent renders a component that owns no pods, only descendants.
func buildGroupingComponent(
	ctx context.Context,
	name, kind string,
	instances []tree.InstanceNode,
	pods []corev1.Pod,
	defs map[string]v1alpha1.ComponentDefinition,
) (ComponentView, error) {
	component := ComponentView{Name: name, Kind: kind}
	for _, instance := range instances {
		for _, node := range instance.Children {
			child, err := buildComponent(ctx, node, defs, pods)
			if err != nil {
				return ComponentView{}, err
			}
			component.Children = appendComponent(component.Children, child, node.HasPodDefinition)
			component.aggregate(child)
		}
	}

	// With descendants the roll-up already carries the declared scale, since it
	// multiplies through to them. With none, only a scale the component really
	// declares is reported: defaulting to one would resurrect the plumbing
	// components appendComponent drops.
	if len(component.Children) == 0 {
		for _, instance := range instances {
			if instance.Scale != nil {
				component.Replicas.Desired += replicasOf(instance.Scale)
			}
		}
	}
	return component, nil
}

// buildPodBearingComponent renders a component that carries pods. pods must
// already be matched to it: the scale and per-replica request come off the spec.
func buildPodBearingComponent(
	ctx context.Context,
	name, kind string,
	instances []tree.InstanceNode,
	pods []corev1.Pod,
	defs map[string]v1alpha1.ComponentDefinition,
) (ComponentView, error) {
	component := ComponentView{Name: name, Kind: kind}
	component.attach(pods)

	for _, instance := range instances {
		replicas := replicasOf(instance.Scale)
		component.Replicas.Desired += replicas
		if instance.ExtractedInstance != nil {
			component.Resources.add(requestOf(*instance.ExtractedInstance).scaled(replicas))
		}

		for _, node := range instance.Children {
			child, err := buildComponent(ctx, node, defs, pods)
			if err != nil {
				return ComponentView{}, err
			}
			component.Children = appendComponent(component.Children, child, node.HasPodDefinition)
			// Only the totals roll up: a child's own pods are its own rows.
			component.Resources.add(child.Resources)
			component.Nodes = mergeNodes(component.Nodes, child.Nodes)
		}
	}
	return component, nil
}

// attach records the pods matched to a component, name-ordered so a re-run
// reads the same.
func (c *ComponentView) attach(pods []corev1.Pod) {
	for i := range pods {
		pod := &pods[i]
		ready := isPodReady(pod)
		c.Replicas.Current++
		if ready {
			c.Replicas.Ready++
		}

		row := PodView{
			Name:      pod.Name,
			Phase:     string(pod.Status.Phase),
			Ready:     ready,
			Resources: podRequest(pod.Spec),
		}
		if node := pod.Spec.NodeName; node != "" {
			row.Node = &node
			c.Nodes = mergeNodes(c.Nodes, []string{node})
		}
		if !ready {
			row.Reason = podReason(pod.Status)
		}
		c.Pods = append(c.Pods, row)
	}
	slices.SortFunc(c.Pods, func(a, b PodView) int { return strings.Compare(a.Name, b.Name) })
}

// appendComponent drops plumbing the workload never populated, such as a
// Deployment's ReplicaSet. A pod-bearing component stays even at zero replicas.
func appendComponent(components []ComponentView, component ComponentView, podBearing bool) []ComponentView {
	if !podBearing && component.isEmpty() {
		return components
	}
	return append(components, component)
}

func (c ComponentView) isEmpty() bool {
	return len(c.Pods) == 0 && len(c.Children) == 0 &&
		c.Replicas.isEmpty() && c.Resources.isEmpty()
}

// aggregate folds a child's totals into a parent that wraps it.
func (c *ComponentView) aggregate(child ComponentView) {
	c.Replicas.Desired += child.Replicas.Desired
	c.Replicas.Current += child.Replicas.Current
	c.Replicas.Ready += child.Replicas.Ready
	c.Resources.add(child.Resources)
	c.Nodes = mergeNodes(c.Nodes, child.Nodes)
}

// podReason explains a pod that is not ready. The scheduler's reason is the
// short form a tree row has space for; a failure falls back to its own.
func podReason(status corev1.PodStatus) string {
	for _, condition := range status.Conditions {
		if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
			return condition.Reason
		}
	}
	if status.Reason != "" {
		return status.Reason
	}
	// A pod that ran and stopped carries no status reason, so the ready
	// condition is the only thing that separates "completed" from "stuck".
	for _, condition := range status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionFalse {
			return condition.Reason
		}
	}
	return ""
}

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func mergeNodes(existing, add []string) []string {
	merged := append(slices.Clone(existing), add...)
	slices.Sort(merged)
	return slices.Compact(merged)
}

// isMultiInstance reports a component whose instances are told apart by an
// instance key or a replica key, and so render as one row each.
func isMultiInstance(instances []tree.InstanceNode) bool {
	for _, instance := range instances {
		if instance.InstanceKey != nil || instance.ReplicaKey != nil {
			return true
		}
	}
	return false
}

func instanceLabel(componentName string, instance tree.InstanceNode) string {
	switch {
	case instance.InstanceKey != nil:
		return *instance.InstanceKey
	case instance.ReplicaKey != nil:
		return fmt.Sprintf("%s[%s]", componentName, *instance.ReplicaKey)
	default:
		return componentName
	}
}

// matchComponentType keeps the pods a component's ComponentTypeSelector accepts.
// claimsAll settles the nil-selector case: with nothing to tell components
// apart, claiming every pod is right for the only one that can own them and
// wrong for a peer that would double-count what a sibling already reported.
func matchComponentType(
	ctx context.Context, selector *v1alpha1.PodSelector, pods []corev1.Pod, claimsAll bool,
) ([]corev1.Pod, error) {
	if selector == nil || selector.ComponentTypeSelector == nil {
		if claimsAll {
			return pods, nil
		}
		// Known gap: a pod no component claims goes unlisted. The view has
		// nowhere to put it, and omitting beats reporting it under a sibling.
		return nil, nil
	}
	var matched []corev1.Pod
	for i := range pods {
		ok, err := resource.NewPodQuerier(&pods[i]).MatchesComponentType(ctx, selector.ComponentTypeSelector)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, pods[i])
		}
	}
	return matched, nil
}

// filterByInstance narrows a component's pods to one instance, by instance id or
// replica key. With neither, every pod reaches every instance.
func filterByInstance(
	ctx context.Context, selector *v1alpha1.PodSelector, instance tree.InstanceNode, pods []corev1.Pod,
) ([]corev1.Pod, error) {
	if selector == nil {
		return pods, nil
	}
	var matched []corev1.Pod
	for i := range pods {
		querier := resource.NewPodQuerier(&pods[i])
		if instance.InstanceKey != nil {
			id, found, err := querier.ExtractInstanceId(ctx, selector.ComponentInstanceSelector)
			if err != nil {
				return nil, err
			}
			if !found || id != *instance.InstanceKey {
				continue
			}
		}
		if instance.ReplicaKey != nil {
			key, found, err := querier.ExtractReplicaKey(ctx, selector.ReplicaSelector)
			if err != nil {
				return nil, err
			}
			if !found || key != *instance.ReplicaKey {
				continue
			}
		}
		matched = append(matched, pods[i])
	}
	return matched, nil
}

// replicasOf reports the desired replica count, defaulting to one for a
// component that declares only a scaling envelope.
func replicasOf(scale *resource.Scale) int32 {
	switch {
	case scale == nil:
		return 1
	case scale.Replicas != nil:
		return *scale.Replicas
	case scale.MinReplicas != nil:
		return *scale.MinReplicas
	default:
		return 1
	}
}

// requestOf sums what one replica of an instance requests. The spec shapes are
// mutually exclusive, so a definition naming several counts only the first.
func requestOf(instance resource.ExtractedInstance) Resources {
	switch {
	case instance.PodTemplateSpec != nil:
		return podRequest(instance.PodTemplateSpec.Spec)
	case instance.PodSpec != nil:
		return podRequest(*instance.PodSpec)
	case instance.FragmentedPodSpec != nil:
		return fragmentedRequest(*instance.FragmentedPodSpec)
	default:
		return Resources{}
	}
}

// fragmentedRequest sums a fragmented spec. A definition may set several of
// these paths at once: Containers and Container name different containers, so
// they add, while Resources states the same pod's request at the CR level and
// only counts when no container declared one.
func fragmentedRequest(spec resource.FragmentedPodSpec) Resources {
	total := sumContainers(spec.Containers)
	if spec.Container != nil {
		total.add(sumContainers([]corev1.Container{*spec.Container}))
	}
	if total.isEmpty() && spec.Resources != nil {
		return requirementsOf(*spec.Resources)
	}
	return total
}

// podRequest mirrors the effective pod request Kubernetes schedules against:
// max(largest init container, regular containers plus sidecars).
func podRequest(spec corev1.PodSpec) Resources {
	running := sumContainers(spec.Containers)

	var largestInit Resources
	for _, container := range spec.InitContainers {
		request := requirementsOf(container.Resources)
		// A sidecar never exits, so it accumulates rather than peaking.
		if container.RestartPolicy != nil && *container.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			running.add(request)
			continue
		}
		largestInit = Resources{
			GPUs:        max(largestInit.GPUs, request.GPUs),
			CPUMillis:   max(largestInit.CPUMillis, request.CPUMillis),
			MemoryBytes: max(largestInit.MemoryBytes, request.MemoryBytes),
		}
	}

	total := Resources{
		GPUs:        max(largestInit.GPUs, running.GPUs),
		CPUMillis:   max(largestInit.CPUMillis, running.CPUMillis),
		MemoryBytes: max(largestInit.MemoryBytes, running.MemoryBytes),
	}

	// The scheduler charges a pod-level CPU or memory request in place of the
	// containers'. An extended resource such as a GPU cannot be declared there.
	if spec.Resources != nil {
		if cpu, ok := quantityOf(*spec.Resources, corev1.ResourceCPU); ok {
			total.CPUMillis = cpu
		}
		if memory, ok := quantityOf(*spec.Resources, corev1.ResourceMemory); ok {
			total.MemoryBytes = memory
		}
	}

	// The runtime's overhead is charged to the pod on top.
	total.add(requirementsOf(corev1.ResourceRequirements{Requests: spec.Overhead}))

	return total
}

func sumContainers(containers []corev1.Container) Resources {
	var total Resources
	for _, container := range containers {
		total.add(requirementsOf(container.Resources))
	}
	return total
}

// requirementsOf falls back to limits: an extended resource such as a GPU is
// often declared there only, and a limit without a request implies it.
func requirementsOf(requirements corev1.ResourceRequirements) Resources {
	gpus, _ := quantityOf(requirements, gpuResourceName)
	cpu, _ := quantityOf(requirements, corev1.ResourceCPU)
	memory, _ := quantityOf(requirements, corev1.ResourceMemory)
	return Resources{GPUs: gpus, CPUMillis: cpu, MemoryBytes: memory}
}

// quantityOf reports whether the resource was declared, so an explicit zero is
// not read as absent.
func quantityOf(requirements corev1.ResourceRequirements, name corev1.ResourceName) (int64, bool) {
	quantity, ok := requirements.Requests[name]
	if !ok {
		if quantity, ok = requirements.Limits[name]; !ok {
			return 0, false
		}
	}
	if name == corev1.ResourceCPU {
		return quantity.MilliValue(), true
	}
	return quantity.Value(), true
}

func kindOf(gvk *metav1.GroupVersionKind) string {
	if gvk == nil {
		return ""
	}
	return gvk.Kind
}
