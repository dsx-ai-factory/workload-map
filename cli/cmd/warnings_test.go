// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
)

var errWriteFailed = errors.New("write failed")

// failingWriter stands in for a closed or full stderr. It returns a sentinel so
// a test can assert the failure is wrapped rather than replaced.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errWriteFailed }

// Loading degrades to the catalog with a note rather than failing, so the note
// is the only thing telling a reader their cluster definitions were not read.
func TestWarningMessagesReachTheReader(t *testing.T) {
	var out bytes.Buffer

	messages := warningMessages([]definitions.Warning{
		{Reason: definitions.ReasonNoCRD, Message: "the Karta CRD is not installed"},
		{Reason: definitions.ReasonCollision, Message: "two definitions claim JobSet"},
	})
	if err := printWarnings(&out, messages); err != nil {
		t.Fatalf("print warnings: %v", err)
	}

	want := "warning: the Karta CRD is not installed\nwarning: two definitions claim JobSet\n"
	if out.String() != want {
		t.Errorf("expected %q, got %q", want, out.String())
	}
}

// A command with nothing to report must stay silent, not emit a bare prefix.
func TestPrintWarningsWritesNothingForNone(t *testing.T) {
	var out bytes.Buffer

	if err := printWarnings(&out, warningMessages(nil)); err != nil {
		t.Fatalf("print warnings: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("expected no output, got %q", out.String())
	}
}

// The messages share the printer with a command's own diagnostics, which are
// already plain strings, so both go through one path.
func TestPrintWarningsTakesPlainMessages(t *testing.T) {
	var out bytes.Buffer

	if err := printWarnings(&out, []string{"JobSet is not installed in this cluster"}); err != nil {
		t.Fatalf("print warnings: %v", err)
	}
	if !strings.HasPrefix(out.String(), "warning: JobSet") {
		t.Errorf("unexpected output: %q", out.String())
	}
}

// A closed pipe must surface, not be swallowed into a silent success.
func TestPrintWarningsReportsAWriteFailure(t *testing.T) {
	err := printWarnings(failingWriter{}, []string{"anything"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, errWriteFailed) {
		t.Errorf("expected the write failure to be wrapped, got %v", err)
	}
}
