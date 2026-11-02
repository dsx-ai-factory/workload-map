// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otel

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// collector formats one immutable result view per collection.
type collector struct {
	store            store.Capturer
	podInfo          gauge
	workloadStatus   gauge
	workloadCreated  gauge
	observationValid gauge
}

type gauge struct {
	name        string
	description string
	labels      []string
	instrument  metric.Float64ObservableGauge
}

type workloadFailure struct {
	ref    store.ObjectRef
	reason string
}

type discoveryFailure struct {
	gvk       schema.GroupVersionKind
	namespace string
	reason    string
}

// Register registers the KEP gauges. Unregister before shutting down the provider.
func Register(meter metric.Meter, s store.Capturer) (metric.Registration, error) {
	c := &collector{
		store: s,
		podInfo: gauge{
			name:        MetricPodWorkloadInfo,
			description: "Pod attribution to a confirmed workload root. Value is always 1.",
			labels:      []string{LabelNamespace, LabelWorkload, LabelWorkloadGroup, LabelWorkloadVersion, LabelWorkloadKind, LabelWorkloadUID, LabelOwnerResolution, LabelPod, LabelPodUID, LabelComponent, LabelComponentInstance, LabelReplicaGroupID, LabelObservationValid},
		},
		workloadStatus: gauge{
			name:        MetricWorkloadStatus,
			description: "Normalized root workload status. Each phase is 0 or 1; several phases can match.",
			labels:      []string{LabelNamespace, LabelWorkload, LabelWorkloadGroup, LabelWorkloadVersion, LabelWorkloadKind, LabelWorkloadUID, LabelPhase, LabelOwnerResolution, LabelKarta, LabelKartaSource, LabelObservationValid},
		},
		workloadCreated: gauge{
			name:        MetricWorkloadCreatedTimestamp,
			description: "Root workload creation time in Unix seconds when its sources and discovery are healthy.",
			labels:      []string{LabelNamespace, LabelWorkload, LabelWorkloadUID},
		},
		observationValid: gauge{
			name:        MetricExporterObservationValid,
			description: "Observed source health and last evaluation outcome for resources, workloads and unresolved discovery. Not application readiness.",
			labels:      []string{"scope", LabelNamespace, LabelResourceGroup, LabelResourceVersion, LabelResourceKind, LabelWorkload, LabelWorkloadUID, LabelReason},
		},
	}
	gauges := []*gauge{&c.podInfo, &c.workloadStatus, &c.workloadCreated, &c.observationValid}
	instruments := make([]metric.Observable, 0, len(gauges))
	for _, g := range gauges {
		instrument, err := meter.Float64ObservableGauge(g.name, metric.WithDescription(g.description))
		if err != nil {
			return nil, fmt.Errorf("create instrument %s: %w", g.name, err)
		}
		g.instrument = instrument
		instruments = append(instruments, instrument)
	}
	return meter.RegisterCallback(c.collect, instruments...)
}

func (c *collector) collect(ctx context.Context, observer metric.Observer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	view := c.store.Capture()
	for key, health := range view.Sources {
		valid, reason := view.Valid([]store.SourceKey{key}, "")
		value := 0.0
		if valid {
			value = 1
		} else {
			reason = failureReason("", reason)
		}
		c.observationValid.observe(observer, value, nil, "resource_type", key.Namespace, health.GVK.Group, health.GVK.Version, health.GVK.Kind, "", "", reason)
	}
	failures := make(map[types.UID]workloadFailure)
	discoveryFailures := make(map[discoveryFailure]struct{})
	for _, failure := range view.Failures {
		if reason := failureReason("", failure.Error); reason != "" {
			discoveryFailures[discoveryFailure{gvk: failure.Input.GVK, namespace: failure.Input.Namespace, reason: reason}] = struct{}{}
		}
	}
	for _, result := range view.Pods {
		if err := ctx.Err(); err != nil {
			return err
		}
		pod := result.Record
		ref := pod.Workload
		valid, reason := view.Valid(result.Sources, result.Error)
		if !valid {
			if ref.UID == "" {
				discoveryFailures[discoveryFailure{gvk: result.Input.GVK, namespace: result.Input.Namespace, reason: failureReason("", reason)}] = struct{}{}
			} else {
				previous := failures[ref.UID]
				// Different API versions can discover the same UID before its root row exists.
				if previous.ref.UID == "" || ref.GVK.String() < previous.ref.GVK.String() {
					previous.ref = ref
				}
				previous.reason = failureReason(previous.reason, reason)
				failures[ref.UID] = previous
			}
		}
		if !result.HasValue {
			continue
		}
		values := []string{pod.Namespace, ref.Name, ref.GVK.Group, ref.GVK.Version, ref.GVK.Kind, string(ref.UID), string(pod.OwnerResolution), pod.Name, string(pod.UID), pod.Component, pod.Instance, pod.Group, strconv.FormatBool(valid)}
		c.podInfo.observe(observer, 1, pod.Labels, values...)
	}
	for _, result := range view.Workloads {
		if err := ctx.Err(); err != nil {
			return err
		}
		workload := result.Record
		ref := workload.Ref
		creationValid, creationReason := view.Valid(result.CreationSources, result.Error)
		statusValid, statusReason := view.Valid(result.Sources, failureReason(result.Error, result.StatusError))
		reason := ""
		if !creationValid {
			reason = failureReason(reason, creationReason)
		}
		if !statusValid {
			reason = failureReason(reason, statusReason)
		}
		previous := failures[workload.UID]
		if previous.reason != "" || reason != "" {
			failures[workload.UID] = workloadFailure{ref: ref, reason: failureReason(previous.reason, reason)}
		}
		if !result.HasValue {
			continue
		}
		if creationValid && workload.CreatedTimestamp > 0 {
			c.workloadCreated.observe(observer, float64(workload.CreatedTimestamp), nil, ref.Namespace, ref.Name, string(workload.UID))
		}
		if workload.HasStatus {
			for _, phase := range allPhases {
				value := 0.0
				if slices.Contains(workload.Phases, string(phase)) {
					value = 1
				}
				values := []string{ref.Namespace, ref.Name, ref.GVK.Group, ref.GVK.Version, ref.GVK.Kind, string(workload.UID), string(phase), string(workload.OwnerResolution), workload.Karta, workload.KartaSource, strconv.FormatBool(statusValid)}
				c.workloadStatus.observe(observer, value, workload.StatusLabels, values...)
			}
		}
	}
	for _, failure := range failures {
		ref := failure.ref
		c.observationValid.observe(observer, 0, nil, "workload", ref.Namespace, ref.GVK.Group, ref.GVK.Version, ref.GVK.Kind, ref.Name, string(ref.UID), failure.reason)
	}
	for failure := range discoveryFailures {
		c.observationValid.observe(observer, 0, nil, "discovery", failure.namespace, failure.gvk.Group, failure.gvk.Version, failure.gvk.Kind, "", "", failure.reason)
	}
	return nil
}

func failureReason(current, next string) string {
	reasons := []string{store.ReasonPermissionDenied, store.ReasonAPIUnavailable, store.ReasonInvalidDefinition, store.ReasonUnresolvedOwner, store.ReasonEvaluationFailed}
	priority := len(reasons)
	for _, reason := range []string{current, next} {
		if reason == "" || reason == store.ReasonNone {
			continue
		}
		index := slices.Index(reasons, reason)
		if index < 0 {
			index = 1 // Unrecognized errors have API-unavailable precedence.
		}
		priority = min(priority, index)
	}
	if priority == len(reasons) {
		return ""
	}
	return reasons[priority]
}

func (g gauge) observe(observer metric.Observer, value float64, custom map[string]string, values ...string) {
	attributes := make([]attribute.KeyValue, 0, len(custom)+len(g.labels))
	for name, value := range custom {
		attributes = append(attributes, attribute.String(name, value))
	}
	for i, name := range g.labels {
		attributes = append(attributes, attribute.String(name, values[i]))
	}
	observer.ObserveFloat64(g.instrument, value, metric.WithAttributeSet(attribute.NewSet(attributes...)))
}
