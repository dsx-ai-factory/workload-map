// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/dsx-ai-factory/workload-map/cli/pkg/definitions"
	"github.com/dsx-ai-factory/workload-map/cli/pkg/generator"
	v1alpha1 "github.com/dsx-ai-factory/workload-map/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/pkg/catalog"
)

const (
	flagGroup   = "group"
	flagKind    = "kind"
	flagVersion = "version"
)

const (
	definitionsUse   = "definitions [NAME]"
	definitionsShort = "List the Karta definitions the CLI understands"

	definitionsLong = `List the Karta definitions the CLI understands, merging the built-in catalog
with the Karta definitions installed in the cluster. A cluster definition overrides a
catalog one describing the same workload type and is listed once, as cluster.
Definitions are not namespaced, and without cluster access the command still lists the
catalog definitions.

Give a NAME to address one definition, or use --group, and optionally --kind and
--version, to narrow the list to one workload type. The table is the human view; json
and yaml carry the definitions themselves, under an items key. A NAME addresses one
definition, so json and yaml emit it on its own instead, and the output applies and
validates as it stands.`

	definitionsExample = `  # Everything the CLI understands (catalog + cluster)
  kli definitions

  # One definition, by the name the list shows
  kli definitions kubeflow-org-pytorchjob-v1

  # Which definition covers JobSet?
  kli definitions --group jobset.x-k8s.io --kind JobSet

  # Dump them as YAML
  kli definitions -o yaml

  # One definition, straight into validate or kubectl
  kli definitions kubeflow-org-pytorchjob-v1 -o yaml | kli validate -`
)

const (
	usageGroup   = "Show the definitions covering this API group"
	usageKind    = "Show the definitions covering this kind; needs --" + flagGroup
	usageVersion = "Show the definitions covering this version; needs --" + flagKind
)

// definitionRow is one row of the table. json and yaml emit the definitions
// themselves, so this never leaves the process and carries no tags.
type definitionRow struct {
	Name       string
	Kind       string
	Origin     string
	Components []string
}

// definitionFilter is the workload type --group, --kind and --version address.
type definitionFilter struct {
	group   string
	kind    string
	version string
	set     bool
}

// newDefinitionsCommand builds the "kli definitions" command. The client getter
// is a parameter so a test can inject a fake without mutating package state.
func newDefinitionsCommand(rcg genericclioptions.RESTClientGetter) *cobra.Command {
	var group, kind, version string
	var (
		output *Enum[generator.Output]
		filter definitionFilter
	)

	cmd := &cobra.Command{
		Use:     definitionsUse,
		Short:   definitionsShort,
		Long:    definitionsLong,
		Example: definitionsExample,
		Args:    usageArgs(cobra.MaximumNArgs(1)),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			var err error
			filter, err = definitionFilterFrom(cmd, args, group, kind, version)
			return err
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			resolver, warnings := definitions.Load(cmd.Context(), rcg)
			if err := printWarnings(cmd.ErrOrStderr(), warningMessages(warnings)); err != nil {
				return err
			}

			matches := resolver.List()
			switch {
			case len(args) == 1:
				def, err := resolver.ByName(args[0])
				if err != nil {
					return fmt.Errorf(
						`no Karta definition named %q. Run "kli definitions" to see what is available`,
						args[0])
				}
				matches = []definitions.Definition{def}

			case filter.set:
				if matches = filter.narrow(matches); len(matches) == 0 {
					return fmt.Errorf(
						`no Karta definition covers %q. Run "kli definitions" to see what is available`,
						filter.String())
				}
			}

			kartas := make([]*v1alpha1.Karta, 0, len(matches))
			for _, def := range matches {
				kartas = append(kartas, def.Karta)
			}
			table := func(out io.Writer) error {
				return renderDefinitions(out, cmd.ErrOrStderr(), definitionRows(matches))
			}
			return generator.Render(cmd.OutOrStdout(), output.Get(), kartas, len(args) == 1, table)
		},
	}

	output = withOutput(cmd, cmd.Flags(), false)
	cmd.Flags().StringVar(&group, flagGroup, "", usageGroup)
	cmd.Flags().StringVar(&kind, flagKind, "", usageKind)
	cmd.Flags().StringVar(&version, flagVersion, "", usageVersion)

	return cmd
}

// definitionFilterFrom rejects a combination that addresses nothing: a kind
// outside a group would match across unrelated APIs, and a NAME addresses the
// same definition the flags do.
func definitionFilterFrom(cmd *cobra.Command, args []string, group, kind, version string) (definitionFilter, error) {
	groupSet := cmd.Flags().Changed(flagGroup)
	kindSet := cmd.Flags().Changed(flagKind)
	versionSet := cmd.Flags().Changed(flagVersion)

	var err error
	switch {
	case len(args) == 1 && groupSet:
		err = fmt.Errorf("a NAME and --%s address the same definition; give one or the other", flagGroup)
	case kindSet && !groupSet:
		err = fmt.Errorf("--%s needs --%s, for example --%s kubeflow.org --%s PyTorchJob",
			flagKind, flagGroup, flagGroup, flagKind)
	case versionSet && !kindSet:
		err = fmt.Errorf("--%s needs --%s, for example --%s kubeflow.org --%s PyTorchJob --%s v1",
			flagVersion, flagKind, flagGroup, flagKind, flagVersion)
	case kindSet && kind == "":
		err = fmt.Errorf("--%s needs a value, for example --%s PyTorchJob", flagKind, flagKind)
	case versionSet && version == "":
		err = fmt.Errorf("--%s needs a value, for example --%s v1", flagVersion, flagVersion)
	}
	if err != nil {
		return definitionFilter{}, usageError(cmd, err)
	}
	return definitionFilter{group: group, kind: kind, version: version, set: groupSet}, nil
}

// narrow returns every definition the filter covers. One kind can be covered at
// several versions, so a filter stopping short of one matches them all.
func (f definitionFilter) narrow(defs []definitions.Definition) []definitions.Definition {
	out := make([]definitions.Definition, 0, len(defs))
	for _, def := range defs {
		gvk := catalog.RootKey(def.Karta)
		// Names no workload type, so no filter covers it.
		if gvk.Version == "" || gvk.Kind == "" {
			continue
		}
		if gvk.Group != f.group {
			continue
		}
		if f.kind != "" && !strings.EqualFold(gvk.Kind, f.kind) {
			continue
		}
		if f.version != "" && gvk.Version != f.version {
			continue
		}
		out = append(out, def)
	}
	return out
}

// String renders the filter the way apimachinery renders a GVK, trimmed to the
// segments given.
func (f definitionFilter) String() string {
	switch {
	case f.version != "":
		return schema.GroupVersionKind{Group: f.group, Version: f.version, Kind: f.kind}.String()
	case f.kind != "":
		return fmt.Sprintf("%s, Kind=%s", f.group, f.kind)
	default:
		return f.group
	}
}

// definitionRows projects definitions into rows sorted by name. Resolver.List
// sorts by GVK, so display order is set here.
func definitionRows(defs []definitions.Definition) []definitionRow {
	rows := make([]definitionRow, 0, len(defs))
	for _, def := range defs {
		structure := def.Karta.Spec.StructureDefinition
		components := make([]string, 0, len(structure.ChildComponents)+1)
		components = append(components, structure.RootComponent.Name)
		for _, child := range structure.ChildComponents {
			components = append(components, child.Name)
		}
		rows = append(rows, definitionRow{
			Name:       def.Karta.Name,
			Kind:       catalog.RootKey(def.Karta).Kind,
			Origin:     string(def.Origin),
			Components: components,
		})
	}
	slices.SortFunc(rows, func(a, b definitionRow) int { return strings.Compare(a.Name, b.Name) })
	return rows
}

// renderDefinitions writes the table. The empty note goes to errOut so stdout
// stays parseable.
func renderDefinitions(out, errOut io.Writer, rows []definitionRow) error {
	if len(rows) == 0 {
		fmt.Fprintln(errOut, "No Karta definitions found.")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 8, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tORIGIN\tCOMPONENTS")
	for _, row := range rows {
		// Still listed, so its author can see the one they wrote.
		kind := row.Kind
		if kind == "" {
			kind = "<none>"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			row.Name, kind, row.Origin, strings.Join(row.Components, ", "))
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write definitions table: %w", err)
	}
	return nil
}
