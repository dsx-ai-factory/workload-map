// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Package cmd wires the Karta CLI Cobra command tree.
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/dsx-ai-factory/workload-map/pkg/version"
)

// NewRootCommand builds the root command for the kli binary. Global flags are
// registered as persistent flags so every subcommand inherits them.
func NewRootCommand() *cobra.Command {
	kubeFlags = genericclioptions.NewConfigFlags(true)

	cmd := &cobra.Command{
		Use:   "kli",
		Short: "Workload-aware visibility for any Kubernetes workload type",
		Long: "Karta gives operators a uniform view of any Kubernetes workload type, " +
			"built on the Karta abstraction layer. Inspect workloads running in a " +
			"namespace and the definitions Karta understands.",
		Version:      version.String(),
		SilenceUsage: true,
		// main prints the error instead, with the lowercase "error:" prefix
		// rather than Cobra's "Error:".
		SilenceErrors: true,
		// Cobra applies this default inside the unexported helper that setting
		// Args below bypasses. At zero only a prefix typo still matches.
		SuggestionsMinimumDistance: 2,
		// Cobra reports an unrecognised subcommand only for a non-runnable
		// command, and before validating args, so the root runs and rejects it.
		Args: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				return nil
			}
			return usageError(c, fmt.Errorf("unknown command %q for %q%s",
				args[0], c.CommandPath(), suggestions(c, args[0])))
		},
		RunE: func(c *cobra.Command, _ []string) error { return c.Help() },
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// The root only prints help, so reading config would turn a bad
			// config file into a reason the reader cannot reach the help.
			if cmd.Name() == cobra.ShellCompRequestCmd || cmd == cmd.Root() {
				return nil
			}
			var err error
			config, err = buildConfig(cmd)
			return err
		},
	}

	cmd.SetVersionTemplate("{{.Version}}\n")

	// Subcommands inherit this from the root, so every flag parse failure in the
	// tree is a usage error without per-command wiring.
	cmd.SetFlagErrorFunc(usageError)

	kubeFlags.AddFlags(cmd.PersistentFlags())
	withOutput(cmd, cmd.PersistentFlags(), true)
	withConfig(cmd)

	cmd.AddCommand(newGetCommand())
	cmd.AddCommand(newDefinitionsCommand(kubeFlags))
	cmd.AddCommand(newValidateCommand())

	cmd.InitDefaultCompletionCmd()
	for _, sub := range cmd.Commands() {
		if sub.Name() == "completion" {
			sub.PersistentPreRunE = func(*cobra.Command, []string) error { return nil }
			break
		}
	}

	return cmd
}

// suggestions renders Cobra's "Did you mean this?" block, which setting Args on
// the root would otherwise drop along with its default argument handling.
func suggestions(cmd *cobra.Command, arg string) string {
	names := cmd.SuggestionsFor(arg)
	if cmd.DisableSuggestions || len(names) == 0 {
		return ""
	}

	var out strings.Builder
	out.WriteString("\n\nDid you mean this?\n")
	for _, name := range names {
		fmt.Fprintf(&out, "\t%v\n", name)
	}
	return out.String()
}
