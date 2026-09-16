// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
)

const (
	flagConfig = "config"
	flagOutput = "output"
)

var kubeFlags *genericclioptions.ConfigFlags

// clusterAccess resolves the cluster connection commands read through. It is a
// variable so tests can point the command tree at a fake cluster.
var clusterAccess = func() genericclioptions.RESTClientGetter { return kubeFlags }

// withOutput registers the -o/--output enum on flags, backed by generator.Output,
// along with its shell completion. A command registering its own on cmd.Flags()
// shadows the root's for that command.
func withOutput(cmd *cobra.Command, flags *pflag.FlagSet, supportsWide bool) *Enum[generator.Output] {
	out := NewOutputFlag(supportsWide)
	flags.VarP(out, flagOutput, "o",
		"Output format: one of "+strings.Join(out.Allowed(), ", "))
	cobra.CheckErr(cmd.RegisterFlagCompletionFunc(flagOutput,
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return out.Allowed(), cobra.ShellCompDirectiveNoFileComp
		}))
	return out
}

// withConfig registers the --config persistent flag on cmd.
func withConfig(cmd *cobra.Command) {
	cmd.PersistentFlags().String(flagConfig, "",
		"Path to the config file (default $KARTA_CONFIG or $HOME/.karta/config.yaml)")
}

// withPhase registers the repeatable --phase enum flag on flags, along with its
// shell completion.
func withPhase(cmd *cobra.Command, flags *pflag.FlagSet) *EnumSlice[string] {
	phase := NewPhaseFlag()
	flags.Var(phase, flagPhase, usagePhase+strings.Join(phase.Allowed(), ", "))
	cobra.CheckErr(cmd.RegisterFlagCompletionFunc(flagPhase,
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return phase.Allowed(), cobra.ShellCompDirectiveNoFileComp
		}))
	return phase
}
