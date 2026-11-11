// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package replay

import (
	"testing"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

func TestValidateRecordingRun(t *testing.T) {
	state := recorder.Event{Kind: recorder.EventState, State: "Running", Phases: []string{"Running"}}
	tests := []struct {
		name      string
		recording recorder.Recording
		wantError bool
	}{
		{
			name: "successful live run",
			recording: recorder.Recording{
				Result: recorder.Result{Succeeded: true},
				Events: []recorder.Event{state},
			},
		},
		{
			name: "failed run with otherwise usable observations",
			recording: recorder.Recording{
				Result: recorder.Result{Failures: []string{"cleanup failed"}},
				Events: []recorder.Event{state},
			},
			wantError: true,
		},
		{
			name: "failure messages override success flag",
			recording: recorder.Recording{
				Result: recorder.Result{Succeeded: true, Failures: []string{"watch lost its position"}},
				Events: []recorder.Event{state},
			},
			wantError: true,
		},
		{
			name:      "missing result cannot establish success",
			recording: recorder.Recording{Events: []recorder.Event{state}},
			wantError: true,
		},
		{
			name:      "empty recording cannot pass vacuously",
			recording: recorder.Recording{Result: recorder.Result{Succeeded: true}},
			wantError: true,
		},
		{
			name: "actions alone are not status evidence",
			recording: recorder.Recording{
				Result: recorder.Result{Succeeded: true},
				Events: []recorder.Event{{Kind: recorder.EventAction}},
			},
			wantError: true,
		},
		{
			name: "expected workload failure is a successful recording",
			recording: recorder.Recording{
				Result: recorder.Result{Succeeded: true},
				Events: []recorder.Event{{Kind: recorder.EventState, State: "Failed", Phases: []string{"Failed"}}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := validateRecordingRun(test.recording); (err != nil) != test.wantError {
				t.Fatalf("validateRecordingRun() = %v; want error %v", err, test.wantError)
			}
		})
	}
}
