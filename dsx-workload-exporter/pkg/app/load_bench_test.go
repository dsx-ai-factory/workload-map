// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package app

import (
	"fmt"
	"io"
	"net/http"
	"runtime"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

// BenchmarkScrape measures one /metrics scrape at the scale test tiers, without a cluster.
func BenchmarkScrape(b *testing.B) {
	for _, pods := range []int{10_000, 50_000, 150_000} {
		b.Run(fmt.Sprintf("pods=%d", pods), func(b *testing.B) {
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			publications := loadTestStore(pods)
			runtime.GC()
			runtime.ReadMemStats(&after)

			h := startHTTPApplication(b, publications, nil)
			h.client.Timeout = 0
			var size int64
			b.ReportAllocs()
			for b.Loop() {
				response, err := h.client.Get("http://" + h.address + "/metrics")
				if err != nil {
					b.Fatal(err)
				}
				size, err = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != http.StatusOK {
					b.Fatalf("scrape: HTTP %d, %v", response.StatusCode, err)
				}
			}
			b.ReportMetric(float64(size), "bytes/scrape")
			b.ReportMetric(float64(after.HeapInuse-before.HeapInuse), "store-heap-bytes")
		})
	}
}

// loadTestStore fills a Store the way the scale test mix does: 4 Pods for each
// workload, spread over 100 namespaces, each with one healthy JobSet source.
func loadTestStore(pods int) store.PublicationStore {
	const podsPerWorkload, namespaces = 4, 100
	gvk := schema.GroupVersionKind{Group: "jobset.x-k8s.io", Version: "v1alpha2", Kind: "JobSet"}
	publications := store.New()
	sources := make([]store.SourceKey, namespaces)
	for i := range sources {
		sources[i] = store.SourceKey{Resource: gvk.GroupVersion().WithResource("jobsets"), Namespace: fmt.Sprintf("karta-load-%d", i)}
		publications.SetSourceHealth(sources[i], store.SourceHealth{GVK: gvk, Synced: true})
	}
	for w := range pods / podsPerWorkload {
		source := sources[w%namespaces]
		root := store.ObjectRef{UID: types.UID(fmt.Sprintf("workload-%d", w)), Namespace: source.Namespace, Name: fmt.Sprintf("train-%d", w), GVK: gvk}
		publications.UpsertWorkload(&store.WorkloadResult{
			Record: store.WorkloadRecord{UID: root.UID, Ref: root, Karta: "jobset-x-k8s-io-jobset-v1alpha2", KartaSource: "catalog",
				OwnerResolution: store.Resolved, HasStatus: true, Phases: []string{"Running"}, CreatedTimestamp: 50},
			HasValue: true, Sources: []store.SourceKey{source}, CreationSources: []store.SourceKey{source},
		})
		for p := range podsPerWorkload {
			publications.UpsertPod(&store.PodResult{Record: store.PodRecord{
				UID: types.UID(fmt.Sprintf("pod-%d-%d", w, p)), Namespace: source.Namespace, Name: fmt.Sprintf("train-%d-workers-0-%d", w, p),
				Workload: root, OwnerResolution: store.Resolved, Component: "replicatedjob", Instance: "workers", Group: "0",
			}, HasValue: true, Sources: []store.SourceKey{source}})
		}
	}
	return publications
}
