// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

const (
	validateUse   = "validate <FILE>"
	validateShort = "Check a Karta definition file before applying it"

	validateLong = `Check a Karta definition file before applying it to a cluster: schema shape,
required fields, and JQ expression validity. Give a path, or "-" to read the definition
from stdin.

Validation is static and fully local, so the command needs no cluster and no kubeconfig.
It reports whether the definition is well formed, not whether it maps a workload
correctly. The exit code is the machine interface: 0 when the definition is valid, 1 when
it is not, and 2 when the file cannot be read as YAML.`

	validateExample = `  # Validate before applying
  kli validate ./my-karta.yaml

  # In a pipeline
  cat my-karta.yaml | kli validate -`
)

func newValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:     validateUse,
		Short:   validateShort,
		Long:    validateLong,
		Example: validateExample,
		Args:    usageArgs(cobra.ExactArgs(1)),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed(flagOutput) {
				return usageError(cmd, fmt.Errorf("--%s is not supported by %s",
					flagOutput, cmd.CommandPath()))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			name, data, err := readDefinition(cmd, args[0])
			if err != nil {
				return usageError(cmd, err)
			}

			var karta v1alpha1.Karta
			if err := yaml.Unmarshal(data, &karta); err != nil {
				return usageError(cmd, fmt.Errorf("parse %s: %w", name, err))
			}

			out := cmd.OutOrStdout()
			if verr := v1alpha1.NewKartaValidator(&karta).Validate(); verr != nil {
				report := verr.Error()
				fmt.Fprintf(out, "INVALID: %s\n  %s\n", name,
					strings.ReplaceAll(report, "\n", "\n  "))
				return fmt.Errorf("%d finding(s) in %s",
					strings.Count(report, "\n")+1, name)
			}

			_, err = fmt.Fprintf(out, "OK: %s is a valid Karta definition (maps %s)\n",
				name, catalog.RootKey(&karta))
			return err
		},
	}
}

func readDefinition(cmd *cobra.Command, path string) (string, []byte, error) {
	if path == "-" {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", nil, fmt.Errorf("read definition from stdin: %w", err)
		}
		return "stdin", data, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read definition: %w", err)
	}
	return path, data, nil
}
