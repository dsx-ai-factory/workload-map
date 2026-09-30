// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"fmt"
	"io"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
)

// printWarnings writes each message with a warning: prefix. Callers pass stderr,
// so a diagnostic never lands in the machine-readable output on stdout.
func printWarnings(out io.Writer, messages []string) error {
	for _, message := range messages {
		if _, err := fmt.Fprintf(out, "warning: %s\n", message); err != nil {
			return fmt.Errorf("write warning: %w", err)
		}
	}
	return nil
}

// warningMessages drops the Reason from each warning. It is there for a caller
// that branches on the kind of failure; one that only prints them wants the
// message alone.
func warningMessages(warnings []definitions.Warning) []string {
	messages := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		messages = append(messages, warning.Message)
	}
	return messages
}
