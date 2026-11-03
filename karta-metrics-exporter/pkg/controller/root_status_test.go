// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	inventory "github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/kartadefinitioninventory"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelevaluator"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/labelresolver"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
)

func TestRootWithoutDefinitionEvaluatesStatusLabels(t *testing.T) {
	for _, gvk := range []schema.GroupVersionKind{lifetimeDeploymentGVK, podGVK} {
		t.Run(gvk.Kind, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			resolver, err := labelresolver.New(labelresolver.Config{Rules: []labelresolver.LabelRule{{
				Name: "team", Metrics: []string{labelresolver.MetricWorkloadStatus},
				Sources: []labelresolver.Source{labelresolver.SourceWorkload}, Expression: `object.metadata.labels.team`,
			}}}, labelresolver.Dependencies{Compiler: labelevaluator.NewCompiler()})
			require.NoError(t, err)
			r.state.deps.Labels = resolver
			w := rootLifetimeWatch(t, r, gvk, "", true)
			root := rootLifetimeObject(gvk, "ns", "root", "root")
			for i, stage := range []struct {
				team      string
				savedTeam string
				reason    string
			}{
				{reason: store.ReasonEvaluationFailed},
				{team: "alpha", savedTeam: "alpha"},
				{savedTeam: "alpha", reason: store.ReasonEvaluationFailed},
				{team: "beta", savedTeam: "beta"},
			} {
				root = root.DeepCopy()
				root.SetResourceVersion(strconv.Itoa(i + 1))
				root.SetLabels(map[string]string{})
				if stage.team != "" {
					root.SetLabels(map[string]string{"team": stage.team})
				}
				require.NoError(t, r.state.applyWatchEvent(w, root, false))
				drainRootLifetimeQueues(t, r)
				result := r.state.deps.Publications.Workload(root.GetUID())
				require.NotNil(t, result)
				require.True(t, result.Record.HasStatus)
				require.Empty(t, result.Record.Phases)
				require.Empty(t, result.Record.Karta)
				require.Empty(t, result.Record.KartaSource)
				require.Equal(t, stage.savedTeam, result.Record.StatusLabels["team"])
				require.Equal(t, stage.reason, result.StatusError)
			}
		})
	}
}

func TestRootDefinitionFailuresDoNotPublishZeroStatus(t *testing.T) {
	definition, err := catalog.Get(lifetimeDeploymentGVK)
	require.NoError(t, err)
	definition.Spec.StructureDefinition.RootComponent.StatusDefinition = &v1alpha1.StatusDefinition{
		PhaseDefinition: &v1alpha1.PhaseDefinition{Path: `error("status failed")`},
	}
	for _, test := range []struct {
		name         string
		selection    selectedDefinition
		inventoryErr error
		reason       string
	}{
		{name: "inventory failure", inventoryErr: inventory.ErrUnavailable, reason: store.ReasonAPIUnavailable},
		{name: "invalid definition", selection: selectedDefinition{err: discovery.ErrInvalidDefinition}, reason: store.ReasonInvalidDefinition},
		{name: "first evaluation failure", selection: selectedDefinition{Selection: inventory.Selection{
			Definition: definition, GVK: lifetimeDeploymentGVK, Origin: inventory.Origin{Name: definition.Name, Source: inventory.SourceCatalog},
		}}, reason: store.ReasonEvaluationFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := newRootLifetimeRuntime(t, Config{})
			w := rootLifetimeWatch(t, r, lifetimeDeploymentGVK, "", true)
			root := rootLifetimeObject(lifetimeDeploymentGVK, "ns", "root", "root")
			require.NoError(t, r.state.applyWatchEvent(w, root, false))
			require.NoError(t, r.resolveSelectedObjectRoot(t.Context(), objectJobFor(refOf(root))))
			r.state.selectedDefinitions[lifetimeDeploymentGVK.GroupKind()] = test.selection
			r.state.definitionInventoryErr = test.inventoryErr
			require.NoError(t, r.reconcileWorkload(t.Context(), root.GetUID()))
			result := r.state.deps.Publications.Workload(root.GetUID())
			require.NotNil(t, result)
			require.True(t, result.HasValue)
			require.False(t, result.Record.HasStatus)
			require.Empty(t, result.Record.Phases)
			require.Equal(t, test.reason, result.StatusError)

			previous := *result
			previous.Record.HasStatus = true
			previous.Record.Karta = "previous"
			previous.Record.Phases = []string{"Running"}
			previous.StatusError = ""
			r.state.deps.Publications.UpsertWorkload(&previous)
			require.NoError(t, r.reconcileWorkload(t.Context(), root.GetUID()))
			result = r.state.deps.Publications.Workload(root.GetUID())
			require.True(t, result.Record.HasStatus)
			require.Equal(t, "previous", result.Record.Karta)
			require.Equal(t, []string{"Running"}, result.Record.Phases)
			require.Equal(t, test.reason, result.StatusError)
		})
	}
}
