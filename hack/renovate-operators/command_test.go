// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"testing"
)

func TestOutputJSON(t *testing.T) {
	for _, test := range []struct {
		name, script, wantError string
		wantValue               int
	}{
		{"success", `printf '{"value":7}'`, "", 7},
		{"invalidJSON", `printf 'invalid'`, "invalid character 'i' looking for beginning of value", 0},
		{"commandFailure", `printf '{"value":7}'; printf 'failed\n' >&2; exit 2`, "sh: failed: exit status 2", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value struct{ Value int }
			err := (executor{}).outputJSON(t.Context(), &value, "sh", "-c", test.script)
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != test.wantError {
				t.Fatalf("outputJSON() error = %v, want %q", err, test.wantError)
			}
			if value.Value != test.wantValue {
				t.Fatalf("decoded value = %d, want %d", value.Value, test.wantValue)
			}
			switch test.name {
			case "invalidJSON":
				if _, ok := err.(*json.SyntaxError); !ok {
					t.Fatalf("decode error must remain unwrapped, got %T", err)
				}
			case "commandFailure":
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 2 {
					t.Fatalf("command error must retain exit status: %v", err)
				}
			}
		})
	}
}
