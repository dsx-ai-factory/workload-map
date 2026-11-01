// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/discovery"
	"github.com/dsx-ai-factory/workload-map/karta-metrics-exporter/pkg/store"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/resource"
)

type workloadStatusAccessor struct {
	resource.ComponentAccessor
	status *resource.Status
	err    error
}

func (a workloadStatusAccessor) ExtractStatus(context.Context, v1alpha1.ComponentDefinition) (*resource.Status, error) {
	return a.status, a.err
}

func TestEvaluateWorkloadStatus(t *testing.T) {
	evaluationErr := errors.New("status evaluation failed")
	for _, tc := range []struct {
		name       string
		status     *resource.Status
		err        error
		wantError  error
		wantPhases []string
	}{
		{name: "matched phases", status: &resource.Status{MatchedStatuses: []v1alpha1.ResourceStatus{v1alpha1.RunningStatus, v1alpha1.FailedStatus}}, wantPhases: []string{"Running", "Failed"}},
		{name: "empty status", status: &resource.Status{}},
		{name: "absent status"},
		{name: "evaluation failure", err: evaluationErr, wantError: discovery.ErrEvaluation},
		{name: "invalid definition", err: discovery.ErrInvalidDefinition, wantError: discovery.ErrInvalidDefinition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := resource.NewComponentFactory(&v1alpha1.Karta{}, workloadStatusAccessor{status: tc.status, err: tc.err})
			record := store.WorkloadRecord{Karta: "selected", KartaSource: "cluster"}
			err := evaluateWorkloadStatus(context.Background(), factory, &record)
			require.ErrorIs(t, err, tc.wantError)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			}
			if tc.wantError == discovery.ErrInvalidDefinition {
				require.NotErrorIs(t, err, discovery.ErrEvaluation)
			}
			require.Equal(t, tc.status != nil, record.HasStatus)
			require.Equal(t, tc.wantPhases, record.Phases)
			require.Equal(t, "selected", record.Karta)
			require.Equal(t, "cluster", record.KartaSource)
		})
	}
}

func TestEvaluateWorkloadStatusComponentFailure(t *testing.T) {
	record := store.WorkloadRecord{}
	err := evaluateWorkloadStatus(context.Background(), &resource.ComponentFactory{}, &record)
	require.ErrorIs(t, err, discovery.ErrEvaluation)
	require.Equal(t, store.WorkloadRecord{}, record)
}
