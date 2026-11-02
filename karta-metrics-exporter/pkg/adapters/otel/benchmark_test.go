// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package otel

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

func BenchmarkCollectIndependentResults(b *testing.B) {
	for _, shape := range []struct{ roots, pods int }{{10, 1}, {1000, 1}, {1, 100000}} {
		b.Run(fmt.Sprintf("roots_%d_pods_%d", shape.roots, shape.pods), func(b *testing.B) {
			s := store.New()
			key := store.SourceKey{Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}}
			s.SetSourceHealth(key, store.SourceHealth{GVK: schema.GroupVersionKind{Version: "v1", Kind: "Pod"}, Synced: true})
			for i := 0; i < shape.roots; i++ {
				root := store.ObjectRef{UID: types.UID(fmt.Sprintf("root-%d", i)), Namespace: "ns", Name: fmt.Sprintf("job-%d", i), GVK: schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}}
				s.UpsertWorkload(&store.WorkloadResult{Record: store.WorkloadRecord{UID: root.UID, Ref: root, HasStatus: true, Phases: []string{"Running"}, CreatedTimestamp: 50}, HasValue: true, Sources: []store.SourceKey{key}, CreationSources: []store.SourceKey{key}})
				for j := 0; j < shape.pods; j++ {
					s.UpsertPod(&store.PodResult{Record: store.PodRecord{UID: types.UID(fmt.Sprintf("pod-%d-%d", i, j)), Name: fmt.Sprintf("worker-%d-%d", i, j), Namespace: "ns", Workload: root}, HasValue: true, Sources: []store.SourceKey{key}})
				}
			}
			reader := sdkmetric.NewManualReader()
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader), sdkmetric.WithCardinalityLimit(0))
			b.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					b.Fatal(err)
				}
			})
			registration, err := Register(provider.Meter("benchmark"), s)
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() {
				if err := registration.Unregister(); err != nil {
					b.Fatal(err)
				}
			})
			var metrics metricdata.ResourceMetrics
			ctx := context.Background()
			if err := reader.Collect(ctx, &metrics); err != nil {
				b.Fatal(err)
			}
			checkBenchmarkCollection(b, metrics, shape.roots, shape.pods)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := reader.Collect(ctx, &metrics); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			checkBenchmarkCollection(b, metrics, shape.roots, shape.pods)
		})
	}
}

// Check the untimed collections so dropped or merged identities cannot improve the result.
func checkBenchmarkCollection(b *testing.B, metrics metricdata.ResourceMetrics, roots, pods int) {
	b.Helper()
	expectedCounts := map[string]int{
		MetricPodWorkloadInfo:          roots * pods,
		MetricWorkloadStatus:           roots * len(allPhases),
		MetricWorkloadCreatedTimestamp: roots,
		MetricExporterObservationValid: 1,
	}
	expectedRoots := make(map[string]struct{}, roots)
	expectedPods := make(map[string]string, roots*pods)
	for i := 0; i < roots; i++ {
		root := fmt.Sprintf("root-%d", i)
		expectedRoots[root] = struct{}{}
		for j := 0; j < pods; j++ {
			expectedPods[fmt.Sprintf("pod-%d-%d", i, j)] = root
		}
	}
	seenFamilies := make(map[string]bool, len(expectedCounts))
	for _, scope := range metrics.ScopeMetrics {
		for _, metric := range scope.Metrics {
			expected, known := expectedCounts[metric.Name]
			if !known || seenFamilies[metric.Name] {
				b.Fatalf("unexpected or repeated metric family %q", metric.Name)
			}
			seenFamilies[metric.Name] = true
			gauge, ok := metric.Data.(metricdata.Gauge[float64])
			if !ok || len(gauge.DataPoints) != expected {
				b.Fatalf("%s: got %T with %d points, want gauge with %d", metric.Name, metric.Data, len(gauge.DataPoints), expected)
			}
			seenIdentities := make(map[string]bool, expected)
			for _, point := range gauge.DataPoints {
				if _, overflow := point.Attributes.Value(attribute.Key("otel.metric.overflow")); overflow {
					b.Fatalf("%s contains an OTel overflow point", metric.Name)
				}
				rootValue, _ := point.Attributes.Value(attribute.Key(LabelWorkloadUID))
				root := rootValue.AsString()
				identity := root
				if metric.Name != MetricExporterObservationValid {
					if _, ok := expectedRoots[root]; !ok {
						b.Fatalf("%s: unexpected workload UID %q", metric.Name, root)
					}
				}
				switch metric.Name {
				case MetricPodWorkloadInfo:
					pod, _ := point.Attributes.Value(attribute.Key(LabelPodUID))
					identity = pod.AsString()
					if expectedPods[identity] != root || point.Value != 1 {
						b.Fatalf("unexpected Pod attribution %q -> %q with value %v", identity, root, point.Value)
					}
				case MetricWorkloadStatus:
					phase, _ := point.Attributes.Value(attribute.Key(LabelPhase))
					knownPhase := false
					for _, expectedPhase := range allPhases {
						knownPhase = knownPhase || phase.AsString() == string(expectedPhase)
					}
					if !knownPhase {
						b.Fatalf("unexpected phase %q", phase.AsString())
					}
					identity += "/" + phase.AsString()
					value := 0.0
					if phase.AsString() == "Running" {
						value = 1
					}
					if point.Value != value {
						b.Fatalf("%s: got value %v, want %v", identity, point.Value, value)
					}
				case MetricWorkloadCreatedTimestamp:
					if point.Value != 50 {
						b.Fatalf("%s: got timestamp %v, want 50", identity, point.Value)
					}
				case MetricExporterObservationValid:
					scope, _ := point.Attributes.Value(attribute.Key("scope"))
					if scope.AsString() != "resource_type" || point.Value != 1 {
						b.Fatalf("unexpected source health: scope %q, value %v", scope.AsString(), point.Value)
					}
				}
				if seenIdentities[identity] {
					b.Fatalf("%s: duplicate identity %q", metric.Name, identity)
				}
				seenIdentities[identity] = true
			}
		}
	}
	if len(seenFamilies) != len(expectedCounts) {
		b.Fatalf("got %d metric families, want %d", len(seenFamilies), len(expectedCounts))
	}
}
