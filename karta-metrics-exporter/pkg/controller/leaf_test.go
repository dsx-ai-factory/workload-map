// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

func TestEvaluatePodRecord(t *testing.T) {
	for _, tc := range []struct {
		name          string
		definition    bool
		malformedPod  bool
		labelFailure  bool
		resolverError bool
	}{
		{name: "mapping before labels", definition: true},
		{name: "no definition skips typed conversion", malformedPod: true},
		{name: "conversion failure skips labels", definition: true, malformedPod: true},
		{name: "label failure retains evaluated mapping", definition: true, labelFailure: true},
		{name: "resolver error", resolverError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := labelresolver.NewMockLabelResolver(t)
			r, values := simpleRuntime(t, resolver)
			selection := selectedDefinition{}
			if tc.definition {
				enableClusterDefinitions(t, r)
				setDefinitions(t, r, definitionObject(t, "job-definition"))
				refreshSelection(t, r)
				var err error
				selection, err = r.state.lookupDefinition(jobKind)
				require.NoError(t, err)
			}
			root, pod := simpleObject(jobKind, "root"), simpleObject(podGVK, "pod")
			simpleOwner(pod, root)
			if tc.malformedPod {
				pod.Object["spec"] = map[string]any{"containers": "not an array"}
			}
			record := store.PodRecord{UID: pod.GetUID(), Component: "<unknown>", Instance: "<unknown>"}
			ctx := context.Background()
			labels := labelresolver.ResolvedMetricLabels{Labels: map[string]string{"team": "example"}}
			if tc.labelFailure {
				labels.Errors = map[string]error{"other": errors.New("invalid label")}
			}
			var resolveErr error
			if tc.resolverError {
				resolveErr = errors.New("resolver unavailable")
			}
			conversionFails := tc.definition && tc.malformedPod
			if !conversionFails {
				resolver.On("Resolve", ctx, labelresolver.Input{
					Metrics: []string{labelresolver.MetricPodWorkloadInfo}, Workload: root, PodObject: pod,
				}).Run(func(args mock.Arguments) {
					input := args.Get(1).(labelresolver.Input)
					require.Same(t, pod, input.PodObject)
					require.Same(t, root, input.Workload)
					require.Nil(t, input.Pod)
					if tc.definition {
						require.Equal(t, "job", record.Component)
						require.Equal(t, "job-definition", record.Karta)
					}
				}).Return([]labelresolver.ResolvedMetricLabels{labels}, resolveErr).Once()
			}

			labelFailed, err := r.evaluatePodRecord(ctx, selection, pod, root, &record)
			switch {
			case conversionFails:
				require.Error(t, err)
			case resolveErr != nil:
				require.ErrorIs(t, err, resolveErr)
			default:
				require.NoError(t, err)
				require.Equal(t, labels.Labels, record.Labels)
			}
			require.Equal(t, tc.labelFailure, labelFailed)
			require.Equal(t, pod.GetUID(), record.UID)
			if !tc.definition {
				require.Equal(t, "<unknown>", record.Component)
				require.Equal(t, "<unknown>", record.Instance)
			}
			require.Nil(t, values.Pod(pod.GetUID()), "evaluation must not publish")
		})
	}
}
