// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package recorder

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kartav1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
)

// observation is one watched run of a flow: the workload being driven, the action steps still to act on, and
// the CRs recorded so far. Its methods do the driving and the recording; Flow.observe fills it in.
type observation struct {
	flow     *Flow
	workload *unstructured.Unstructured

	pending   []journeyStep              // action steps not yet reached, in order
	snapshots []snapshot                 // the distinct CRs kept, in order
	lastSig   map[string]any             // last kept CR minus volatile fields, for dedup
	lastSeen  *unstructured.Unstructured // most recent CR, shown in triage on timeout
	failure   string                     // why the flow did not finish, empty if it did
}

// snapshot is one recorded CR: the phases read from its own fields (state is the strongest of them), the
// object, and any action performed at it.
type snapshot struct {
	state                   kartav1alpha1.ResourceStatus
	phases                  []kartav1alpha1.ResourceStatus
	cr                      *unstructured.Unstructured
	action                  *RecordedAction
	staleObservedGeneration bool // the controller had not observed the spec yet; recorded, but outside the order-checked walk
}

// watchAndAct watches the workload until the flow finishes or fails, recording each CR it sees and acting
// on the journey steps as they are reached. A watch that cannot resume fails the run.
func (o *observation) watchAndAct(ctx context.Context) {
	watcher, err := o.flow.startWatch(ctx, o.workload)
	if err != nil {
		o.failure = err.Error()
		return
	}
	defer watcher.Stop()

	var lastWatchErr error
	for {
		select {
		case <-ctx.Done():
			o.failure = fmt.Sprintf("did not reach %q within %s; observed %v\nlast-seen status:\n%s",
				o.flow.terminalState(), o.flow.rec.timeout, o.states(), dumpStatus(o.lastSeen))
			return
		case event, open := <-watcher.ResultChan():
			switch {
			case !open:
				// The RetryWatcher resumes transient drops itself; it gives up only when it cannot resume
				// from its position (410 Gone, the history was compacted). Fail fast; a human re-records.
				o.failure = fmt.Sprintf("watch lost its position and cannot resume; last watch error: %v; observed %v; re-record the flow\nlast-seen status:\n%s",
					lastWatchErr, o.states(), dumpStatus(o.lastSeen))
				return
			case event.Type == watch.Error:
				lastWatchErr = apierrors.FromObject(event.Object)
			default:
				cr, ok := event.Object.(*unstructured.Unstructured)
				if !ok {
					o.failure = fmt.Sprintf("watch delivered a %T instead of the workload; observed %v",
						event.Object, o.states())
					return
				}
				if o.record(ctx, cr) {
					return
				}
			}
		}
	}
}

// startWatch starts a resilient watch of the workload by name that resumes after transient drops.
func (f *Flow) startWatch(ctx context.Context, workload *unstructured.Unstructured) (watch.Interface, error) {
	gvk := workload.GroupVersionKind()
	mapping, err := f.client().RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, fmt.Errorf("rest mapping for %s: %w", gvk, err)
	}
	namespace := workload.GetNamespace()
	if namespace == "" {
		return nil, fmt.Errorf("workload %s has no namespace", workload.GetName())
	}

	startRV := workload.GetResourceVersion()
	if startRV == "" {
		return nil, fmt.Errorf("workload %s has no resourceVersion to watch from", workload.GetName())
	}

	return watchtools.NewRetryWatcherWithContext(ctx, startRV, &cache.ListWatch{
		WatchFuncWithContext: func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fields.OneTermEqualSelector("metadata.name", workload.GetName()).String()
			return f.rec.config.Cluster.Dynamic.Resource(mapping.Resource).Namespace(namespace).Watch(ctx, opts)
		},
	})
}

// record handles one seen CR: it keeps every distinct frame, but acts on the action steps and the terminal only
// once the controller has observed the current spec. stop=true means stop watching - the flow reached its
// terminal state, or an action failed (the failure itself is in o.failure).
func (o *observation) record(ctx context.Context, cr *unstructured.Unstructured) (stop bool) {
	o.lastSeen = cr
	phases := judge(cr, o.flow.rec.states)
	state := Strongest(phases)
	observed := hasObservedCurrentGeneration(cr)
	o.keep(cr, phases, observed)
	if !observed {
		return false
	}
	if err := o.advanceStep(ctx, state, cr); err != nil {
		o.failure = err.Error()
		return true
	}
	return o.hasReachedTerminal(state)
}

// keep appends cr as a new snapshot, unless it duplicates the last kept one (same content once the volatile
// fields are stripped).
func (o *observation) keep(cr *unstructured.Unstructured, phases []kartav1alpha1.ResourceStatus, observed bool) {
	sig := stripVolatileFields(cr)
	if o.lastSig != nil && reflect.DeepEqual(o.lastSig, sig) {
		return
	}
	o.lastSig = sig
	o.snapshots = append(o.snapshots, snapshot{
		state:                   Strongest(phases),
		phases:                  phases,
		cr:                      cr.DeepCopy(),
		staleObservedGeneration: !observed,
	})
}

// advanceStep performs the next pending step's action if the workload just reached its state and gate, then
// drops it from pending. A failed action is returned as the error and ends the run.
func (o *observation) advanceStep(ctx context.Context, state kartav1alpha1.ResourceStatus, cr *unstructured.Unstructured) error {
	next := o.pending
	reached := len(next) > 0 && state == next[0].State &&
		(next[0].ReachedWhen == nil || next[0].ReachedWhen(cr))
	if !reached {
		return nil
	}
	if next[0].Action != nil {
		action, err := o.flow.performAction(ctx, o.workload, next[0].Action)
		if err != nil {
			return err
		}
		o.recordAction(action)
	}
	o.pending = next[1:]
	return nil
}

// performAction performs the action's operation on the workload and returns the recorded operation. The
// result object is not captured - the STATE events that follow show where the operator drives it.
func (f *Flow) performAction(ctx context.Context, workload *unstructured.Unstructured, action *Action) (*RecordedAction, error) {
	target := blankWithGVK(workload)
	target.SetName(workload.GetName())
	target.SetNamespace(workload.GetNamespace())
	if err := f.client().Patch(ctx, target, client.RawPatch(types.MergePatchType, action.Patch)); err != nil {
		return nil, fmt.Errorf("action %s on %s/%s: %w", action.Type, workload.GetNamespace(), workload.GetName(), err)
	}

	var payload map[string]any
	if err := json.Unmarshal(action.Patch, &payload); err != nil {
		return nil, fmt.Errorf("action %s payload: %w", action.Type, err)
	}
	return &RecordedAction{
		Name:      string(action.Type),
		Operation: Operation{Verb: "PATCH", PatchType: "application/merge-patch+json", Payload: payload},
	}, nil
}

// recordAction stores the action on the snapshot just kept; a checkpoint only fires after record kept a
// frame, so snapshots is never empty here.
func (o *observation) recordAction(action *RecordedAction) {
	o.snapshots[len(o.snapshots)-1].action = action
}

// hasReachedTerminal reports whether state is the flow's terminal state and no action steps remain.
func (o *observation) hasReachedTerminal(state kartav1alpha1.ResourceStatus) bool {
	return state == o.flow.terminalState() && len(o.pending) == 0
}

// states is the ordered sequence of observed-frame states - the walk the order check validates.
func (o *observation) states() []kartav1alpha1.ResourceStatus {
	var out []kartav1alpha1.ResourceStatus
	for _, s := range o.snapshots {
		if !s.staleObservedGeneration {
			out = append(out, s.state)
		}
	}
	return out
}
