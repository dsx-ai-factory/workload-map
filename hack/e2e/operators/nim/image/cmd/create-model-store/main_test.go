// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name      string
		delay     string
		fail      string
		wantError bool
	}{
		{name: "writes marker"},
		{name: "accepts zero delay", delay: "0s"},
		{name: "controlled failure", fail: "true", wantError: true},
		{name: "invalid delay", delay: "invalid", wantError: true},
		{name: "negative delay", delay: "-1s", wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("NIM_CACHE_TEST_DELAY", tc.delay)
			t.Setenv("NIM_CACHE_TEST_FAIL", tc.fail)
			store := filepath.Join(t.TempDir(), "model-store")
			err := run([]string{"--model-repo", "example/model", "--model-store", store})
			if (err != nil) != tc.wantError {
				t.Fatalf("run error = %v, wantError = %v", err, tc.wantError)
			}
			if tc.fail == "true" && !errors.Is(err, errSimulatedFailure) {
				t.Fatalf("expected controlled failure, got %v", err)
			}
			data, readErr := os.ReadFile(filepath.Join(store, "fictive-model.txt"))
			if tc.wantError {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("failed run wrote a marker: %v", readErr)
				}
				return
			}
			if readErr != nil || string(data) != "example/model\n" {
				t.Fatalf("marker = %q, error = %v", data, readErr)
			}
		})
	}
}

func TestRunRejectsMissingArguments(t *testing.T) {
	if err := run(nil); err == nil {
		t.Fatal("expected missing model arguments to fail")
	}
}
