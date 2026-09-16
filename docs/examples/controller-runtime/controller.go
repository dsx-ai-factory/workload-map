// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/resource"
)

// Annotation keys written back to every reconciled workload. The controller
// never references a CRD-specific field path to compute these values — Karta
// resolves them from the structure definition stored in the cluster.
const (
	annotationStatus   = "karta/status"
	annotationReplicas = "karta/replicas"
	annotationCPU      = "karta/cpu-request"
	annotationMemory   = "karta/memory-request"
	annotationGPU      = "karta/gpu-request"

	// managedByLabel is injected into every pod-bearing component's template through Karta.
	managedByLabelKey   = "app.kubernetes.io/managed-by"
	managedByLabelValue = "karta"
	gpuResourceName     = "nvidia.com/gpu"
)

// parseGVKs parses a comma-separated list of "group/version/kind" entries into
// the set of workload types to manage. The core group is empty, so a Pod is
// "/v1/Pod". Each parsed GVK gets its own reconciler instance; the reconcile
// logic is identical for every entry because Karta absorbs the structural
// differences. Managing a new workload type needs no code change: add its GVK to
// this list (via the --watch-gvk flag) and apply its Karta object.
func parseGVKs(raw string) ([]schema.GroupVersionKind, error) {
	var gvks []schema.GroupVersionKind
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, "/")
		if len(parts) != 3 || parts[1] == "" || parts[2] == "" {
			return nil, fmt.Errorf("invalid GVK %q: want group/version/kind (core group is empty, e.g. /v1/Pod)", entry)
		}
		gvks = append(gvks, schema.GroupVersionKind{Group: parts[0], Version: parts[1], Kind: parts[2]})
	}
	if len(gvks) == 0 {
		return nil, fmt.Errorf("no workload GVKs configured")
	}
	return gvks, nil
}

// WorkloadReconciler inspects and mutates one workload type through a Karta
// definition that lives in the cluster as a Karta custom resource. The same
// struct serves every GVK; only the GVK field differs between instances.
type WorkloadReconciler struct {
	client.Client
	Recorder events.EventRecorder
	GVK      schema.GroupVersionKind
}

// Reconcile reacts to every change of a watched workload (and to changes of the
// Karta definition itself). The body contains no switch on workload kind: the
// Karta object absorbs all structural differences, wherever the pod templates
// live (for a LeaderWorkerSet, in its leader and worker child components).
func (r *WorkloadReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Step 0: discover the workload structure from the cluster by GVK. The
	// controller does not assume a Karta name: it selects the Karta whose root
	// component matches this workload's GVK, the same way a real consumer
	// resolves a definition for an arbitrary workload type.
	karta, err := r.kartaForGVK(ctx, r.GVK)
	if err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("no Karta describes this workload; apply one before workloads", "gvk", r.GVK.String())
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	// Step 1: fetch the workload as an unstructured object. Remember the status
	// the controller recorded last time so we can detect a transition below.
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(r.GVK)
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil // deleted; nothing to do
		}
		return reconcile.Result{}, fmt.Errorf("get %s %s: %w", r.GVK.Kind, req.NamespacedName, err)
	}
	previousStatus := obj.GetAnnotations()[annotationStatus]

	// Step 2: the single Karta entry point for all read and write operations.
	factory := resource.NewComponentFactoryFromObject(karta, obj)
	components, err := allComponents(factory)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Step 3: inspect status, scale and resource requests uniformly across every
	// component (root and children), regardless of where pods are defined.
	summary, err := inspect(ctx, factory, components)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Step 4: inject the managed-by label into every pod-bearing component
	// through Karta. Karta routes each update to the right path in the workload.
	labelChanged, err := injectPodTemplateLabels(ctx, components)
	if err != nil {
		return reconcile.Result{}, err
	}
	if labelChanged {
		// GetResource hands back the full mutated object (all component updates
		// applied); continue on it so a single Update persists both the labels
		// and the annotations below.
		mutated, err := factory.GetResource()
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("get mutated resource: %w", err)
		}
		obj = mutated.(*unstructured.Unstructured)
		r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "PodTemplateLabeled", "InjectLabel",
			"Injected pod-template label %s=%s via Karta", managedByLabelKey, managedByLabelValue)
	}

	// Step 5: report the inspection on the workload's own metadata. Object
	// annotations are always mutable, so this persists on any workload.
	annotationsChanged := applyAnnotations(obj, summary)

	if !annotationsChanged && !labelChanged {
		return reconcile.Result{}, nil // idempotent: nothing to write
	}

	if err := r.Update(ctx, obj); err != nil {
		if apierrors.IsConflict(err) {
			return reconcile.Result{Requeue: true}, nil
		}
		return reconcile.Result{}, fmt.Errorf("update %s %s: %w", r.GVK.Kind, req.NamespacedName, err)
	}

	// Emit a status event only on a real transition between two known statuses.
	// Undefined moments are skipped (their status key was omitted), so this never reports phantom changes.
	if newStatus := summary[annotationStatus]; newStatus != "" {
		if previousStatus == "" {
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "StatusRecorded", "Inspect",
				"Recorded initial workload status: %s (replicas=%s cpu=%s memory=%s gpu=%s)",
				newStatus, summary[annotationReplicas],
				summary[annotationCPU], summary[annotationMemory], summary[annotationGPU])
		}
		if previousStatus != "" && newStatus != previousStatus {
			r.Recorder.Eventf(obj, nil, corev1.EventTypeNormal, "StatusChanged", "Inspect",
				"Workload status changed: %s -> %s (replicas=%s cpu=%s memory=%s gpu=%s)",
				previousStatus, newStatus, summary[annotationReplicas],
				summary[annotationCPU], summary[annotationMemory], summary[annotationGPU])
		}
	}
	return reconcile.Result{}, nil
}

// allComponents returns every component of the workload (children plus root),
// mirroring how a real consumer walks the full structure rather than assuming
// pods live on the root.
func allComponents(factory *resource.ComponentFactory) ([]*resource.Component, error) {
	children, err := factory.GetChildComponents()
	if err != nil {
		return nil, fmt.Errorf("get child components: %w", err)
	}
	root, err := factory.GetRootComponent()
	if err != nil {
		return nil, fmt.Errorf("get root component: %w", err)
	}
	return append(children, root), nil
}

// inspect reads the unified status from the root and aggregates replica counts
// and resource requests across all components. It returns the annotation summary
// to write back to the workload.
func inspect(ctx context.Context, factory *resource.ComponentFactory, components []*resource.Component) (map[string]string, error) {
	root, err := factory.GetRootComponent()
	if err != nil {
		return nil, fmt.Errorf("get root component: %w", err)
	}
	status, err := root.GetStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("get status: %w", err)
	}

	matched := make(map[v1alpha1.ResourceStatus]bool, len(status.MatchedStatuses))
	for _, s := range status.MatchedStatuses {
		matched[s] = true
	}

	replicas := int32(0)
	cpu, mem, gpu := apiresource.Quantity{}, apiresource.Quantity{}, apiresource.Quantity{}
	for _, comp := range components {
		scales, err := comp.GetScale(ctx)
		if err != nil {
			return nil, fmt.Errorf("get scale for %s: %w", comp.Name(), err)
		}
		for _, scale := range scales {
			if scale.Replicas != nil {
				replicas += *scale.Replicas
			}
		}

		if !comp.HasPodDefinition() {
			continue
		}
		podTemplateSpecs, err := comp.GetPodTemplateSpec(ctx)
		if err != nil {
			return nil, fmt.Errorf("get pod template spec for %s: %w", comp.Name(), err)
		}
		for _, pts := range podTemplateSpecs {
			for _, c := range pts.Spec.Containers {
				req := c.Resources.Requests
				cpu.Add(*req.Cpu())
				mem.Add(*req.Memory())
				if g, ok := req[corev1.ResourceName(gpuResourceName)]; ok {
					gpu.Add(g)
				} else if g, ok := c.Resources.Limits[corev1.ResourceName(gpuResourceName)]; ok {
					gpu.Add(g)
				}
			}
		}
	}

	summary := map[string]string{
		annotationReplicas: fmt.Sprintf("%d", replicas),
		annotationCPU:      cpu.String(),
		annotationMemory:   mem.String(),
		annotationGPU:      gpu.String(),
	}
	// Record a single canonical status. When no mapping matches (an undefined
	// moment between real states) the status key is omitted, so the controller
	// keeps the last known status instead of flapping through "Undefined".
	if canonical := canonicalStatus(matched); canonical != "" {
		summary[annotationStatus] = canonical
	}
	return summary, nil
}

// statusPriority orders Karta statuses from most to least significant. A
// workload can match several mappings at once; the controller reports only the
// highest-priority one so callers see a single, stable phase.
var statusPriority = []v1alpha1.ResourceStatus{
	v1alpha1.FailedStatus,
	v1alpha1.CompletedStatus,
	v1alpha1.RunningStatus,
	v1alpha1.InitializingStatus,
	v1alpha1.SuspendedStatus,
	v1alpha1.SuspendingStatus,
	v1alpha1.DegradedStatus,
}

// canonicalStatus returns the highest-priority matched status, or "" when none
// matched.
func canonicalStatus(matched map[v1alpha1.ResourceStatus]bool) string {
	for _, s := range statusPriority {
		if matched[s] {
			return string(s)
		}
	}
	return ""
}

// applyAnnotations writes the summary onto the object and reports whether any
// value actually changed, so the caller can avoid no-op updates.
func applyAnnotations(obj *unstructured.Unstructured, summary map[string]string) bool {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	changed := false
	for k, v := range summary {
		if annotations[k] != v {
			annotations[k] = v
			changed = true
		}
	}
	if changed {
		obj.SetAnnotations(annotations)
	}
	return changed
}

// injectPodTemplateLabels adds the managed-by label to every pod template of
// every pod-bearing component through Karta, regardless of where the templates
// live in the underlying CRD. It reports whether any template actually changed
// so callers can skip a no-op Update.
func injectPodTemplateLabels(ctx context.Context, components []*resource.Component) (bool, error) {
	changed := false
	for _, comp := range components {
		if !comp.HasPodDefinition() {
			continue
		}
		podTemplateSpecs, err := comp.GetPodTemplateSpec(ctx)
		if err != nil {
			return false, fmt.Errorf("get pod template spec for %s: %w", comp.Name(), err)
		}
		compChanged := false
		updates := make(map[string]corev1.PodTemplateSpec, len(podTemplateSpecs))
		for id, pts := range podTemplateSpecs {
			if pts.Labels[managedByLabelKey] != managedByLabelValue {
				if pts.Labels == nil {
					pts.Labels = map[string]string{}
				}
				pts.Labels[managedByLabelKey] = managedByLabelValue
				compChanged = true
			}
			updates[id] = pts
		}
		if !compChanged {
			continue
		}
		if err := comp.UpdatePodTemplateSpec(ctx, updates); err != nil {
			return false, fmt.Errorf("update pod template spec for %s: %w", comp.Name(), err)
		}
		changed = true
	}
	return changed, nil
}

// kartaForGVK selects the Karta whose root component describes the given GVK.
// This mirrors how a real consumer resolves a definition for an arbitrary
// workload type instead of hard-coding a Karta name.
func (r *WorkloadReconciler) kartaForGVK(ctx context.Context, gvk schema.GroupVersionKind) (*v1alpha1.Karta, error) {
	list := &v1alpha1.KartaList{}
	if err := r.List(ctx, list); err != nil {
		return nil, fmt.Errorf("list Kartas: %w", err)
	}
	for i := range list.Items {
		if rootGVK := rootComponentGVK(&list.Items[i]); rootGVK != nil && *rootGVK == gvk {
			return &list.Items[i], nil
		}
	}
	return nil, apierrors.NewNotFound(v1alpha1.Resource("karta"), fmt.Sprintf("for gvk %s", gvk))
}

// rootComponentGVK returns the GVK of a Karta's root component, or nil if unset.
func rootComponentGVK(karta *v1alpha1.Karta) *schema.GroupVersionKind {
	kind := karta.Spec.StructureDefinition.RootComponent.Kind
	if kind == nil {
		return nil
	}
	return &schema.GroupVersionKind{Group: kind.Group, Version: kind.Version, Kind: kind.Kind}
}

// SetupWithManager wires the reconciler to watch its workload type and the Karta
// definitions. A change to a Karta object re-enqueues every workload it governs
// so the new structure takes effect live, with no redeploy.
func (r *WorkloadReconciler) SetupWithManager(mgr ctrl.Manager) error {
	workloadType := &unstructured.Unstructured{}
	workloadType.SetGroupVersionKind(r.GVK)
	controllerName := strings.ToLower(strings.Trim(
		fmt.Sprintf("%s-%s-%s", r.GVK.Group, r.GVK.Version, r.GVK.Kind), "-",
	))
	return ctrl.NewControllerManagedBy(mgr).
		For(workloadType).
		Named(controllerName).
		Watches(&v1alpha1.Karta{}, handler.EnqueueRequestsFromMapFunc(r.workloadsForKarta)).
		Complete(r)
}

// workloadsForKarta enqueues all workloads of this reconciler's GVK when a Karta
// describing that GVK changes.
func (r *WorkloadReconciler) workloadsForKarta(ctx context.Context, obj client.Object) []reconcile.Request {
	karta, ok := obj.(*v1alpha1.Karta)
	if !ok {
		return nil
	}
	if rootGVK := rootComponentGVK(karta); rootGVK == nil || *rootGVK != r.GVK {
		return nil
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(r.GVK.GroupVersion().WithKind(r.GVK.Kind + "List"))
	if err := r.List(ctx, list); err != nil {
		log.FromContext(ctx).Error(err, "list workloads for Karta change",
			"gvk", r.GVK.String(), "karta", karta.GetName())
		return nil
	}
	requests := make([]reconcile.Request, 0, len(list.Items))
	for i := range list.Items {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: list.Items[i].GetNamespace(),
				Name:      list.Items[i].GetName(),
			},
		})
	}
	return requests
}
