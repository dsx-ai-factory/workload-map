// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"time"

	kubediscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/flowcontrol"

	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/dsx-workload-exporter/pkg/store"
)

// New constructs the exporter components. The application starts their workers.
func New(cfg config.Config, kubeConfig *rest.Config) (app.Components, error) {
	if err := cfg.Validate(); err != nil {
		return app.Components{}, err
	}
	if kubeConfig == nil {
		return app.Components{}, errors.New("kubernetes configuration is required")
	}
	timeout, err := time.ParseDuration(cfg.Tuning.APIClient.RequestTimeout)
	if err != nil {
		return app.Components{}, fmt.Errorf("tuning.apiClient.requestTimeout: %w", err)
	}
	kubeConfig = rest.CopyConfig(kubeConfig)
	if kubeConfig.WarningHandler == nil && kubeConfig.WarningHandlerWithContext == nil {
		// Namespace watches can receive the same API warning on each connection.
		kubeConfig.WarningHandler = rest.NewWarningWriter(os.Stderr, rest.WarningWriterOptions{Deduplicate: true})
	}
	kubeConfig.QPS = cfg.Tuning.APIClient.QPS
	kubeConfig.Burst = cfg.Tuning.APIClient.Burst
	kubeConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(cfg.Tuning.APIClient.QPS, cfg.Tuning.APIClient.Burst)
	dynamicClient, err := dynamic.NewForConfig(kubeConfig)
	if err != nil {
		return app.Components{}, err
	}
	metadataClient, err := metadata.NewForConfig(kubeConfig)
	if err != nil {
		return app.Components{}, err
	}
	discoveryClient, err := kubediscovery.NewDiscoveryClientForConfig(kubeConfig)
	if err != nil {
		return app.Components{}, err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discoveryClient))
	reader, err := controller.NewReader(controller.Clients{
		Dynamic:   dynamicClient,
		Metadata:  metadataClient,
		Mapper:    mapper,
		Discovery: discoveryClient,
	}, controller.ReaderOptions{RequestTimeout: timeout})
	if err != nil {
		return app.Components{}, err
	}
	definitionConfig := cfg.KartaDefinitionSource.InventoryConfig()
	definitionConfig.RequestTimeout = timeout
	definitions, err := inventory.New(dynamicClient, definitionConfig)
	if err != nil {
		return app.Components{}, err
	}
	ownership, err := discovery.New(reader, discovery.Config{IgnoredTopOwners: cfg.Selection.IgnoredOwnerKinds})
	if err != nil {
		return app.Components{}, err
	}
	labels, err := labelresolver.New(cfg.Metrics.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	if err != nil {
		return app.Components{}, err
	}
	publications := store.New()
	runtime, err := controller.New(controller.Config{
		IgnoredOwnerKinds:      cfg.Selection.IgnoredOwnerKinds,
		ResourceRules:          cfg.Selection.IncludeRootResources,
		ExcludeResourceRules:   cfg.Selection.ExcludeRootResources,
		NamespaceSelector:      cfg.Selection.NamespaceSelector,
		ObjectSelectors:        cfg.Selection.RootSelectors,
		PodSelector:            cfg.Selection.PodSelector,
		WorkloadMetricSelector: cfg.Metrics.WorkloadSelector,
		PodWorkloadAssociation: cfg.Metrics.PodWorkloadAssociation,
		ClusterDefinitions:     cfg.KartaDefinitionSource.Cluster.Enabled,
		RequiredKinds:          cfg.Selection.RequiredWatchKinds,
		Workers:                cfg.Tuning.Workers.Default,
		LeafWorkers:            cfg.Tuning.Workers.Pods,
		RootWorkers:            cfg.Tuning.Workers.Workloads,
	}, controller.Dependencies{
		Discovery:    ownership,
		Inventory:    definitions,
		Labels:       labels,
		Publications: publications,
	}, reader)
	if err != nil {
		return app.Components{}, err
	}
	return app.Components{Runtime: runtime, Store: publications, Definitions: definitions, ExcludeStatuses: cfg.Metrics.ExcludeStatuses}, nil
}
