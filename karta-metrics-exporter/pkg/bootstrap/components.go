// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package bootstrap

import (
	"errors"
	"fmt"
	"time"

	kubediscovery "k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/util/flowcontrol"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/app"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/config"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/controller"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
)

// New constructs the exporter components. The application starts their workers.
func New(cfg config.Config, kubeConfig *rest.Config) (app.Components, error) {
	if err := cfg.Validate(); err != nil {
		return app.Components{}, err
	}
	if kubeConfig == nil {
		return app.Components{}, errors.New("kubernetes configuration is required")
	}
	timeout, err := time.ParseDuration(cfg.RequestTimeout)
	if err != nil {
		return app.Components{}, fmt.Errorf("requestTimeout: %w", err)
	}
	kubeConfig = rest.CopyConfig(kubeConfig)
	kubeConfig.QPS = cfg.APIQPS
	kubeConfig.Burst = cfg.APIBurst
	kubeConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(cfg.APIQPS, cfg.APIBurst)
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
	reader, err := controller.NewReader(controller.Clients{Dynamic: dynamicClient, Metadata: metadataClient, Mapper: mapper}, controller.ReaderOptions{RequestTimeout: timeout})
	if err != nil {
		return app.Components{}, err
	}
	definitionConfig := cfg.KartaDefinitionSource.InventoryConfig()
	definitionConfig.RequestTimeout = timeout
	definitions, err := inventory.New(dynamicClient, definitionConfig)
	if err != nil {
		return app.Components{}, err
	}
	ownership, err := discovery.New(reader, discovery.Config{IgnoredTopOwners: cfg.IgnoredOwnerKinds})
	if err != nil {
		return app.Components{}, err
	}
	labels, err := labelresolver.New(cfg.Config, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
	if err != nil {
		return app.Components{}, err
	}
	publications := store.New()
	runtime, err := controller.New(controller.Config{IgnoredOwnerKinds: cfg.IgnoredOwnerKinds, ResourceRules: cfg.ResourceRules, ExcludeResourceRules: cfg.ExcludeResourceRules, NamespaceSelector: cfg.NamespaceSelector, ObjectSelector: cfg.ObjectSelector, PodSelector: cfg.PodSelector, ClusterDefinitions: cfg.KartaDefinitionSource.Cluster.Enabled, RequiredKinds: cfg.RequiredKinds, Workers: cfg.Workers, LeafWorkers: cfg.LeafWorkers, RootWorkers: cfg.RootWorkers}, controller.Dependencies{Discovery: ownership, Inventory: definitions, Labels: labels, Publications: publications}, reader)
	if err != nil {
		return app.Components{}, err
	}
	return app.Components{Runtime: runtime, Store: publications, Definitions: definitions}, nil
}
