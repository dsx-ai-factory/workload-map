// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2/reporters"
	"github.com/onsi/ginkgo/v2/types"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

type verificationFiles struct {
	expected  string
	actual    string
	installed string
	root      string
	labels    string
}

func TestVerifySuccessfulRecordings(t *testing.T) {
	for _, labels := range []string{"jobset", "kubeflow", "kserve", "kuberay", "lws,grove,dynamo,nim,agent-sandbox,knative,milvus"} {
		t.Run(labels, func(t *testing.T) {
			files := writeVerificationFiles(t, labels)
			if err := verify(files.expected, files.actual, files.installed, labels, files.root); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerifyRejectsIncompleteExecution(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected bool
		change   func(*types.Report)
	}{
		{"live dry run", false, func(r *types.Report) { r.SuiteConfig.DryRun = true }},
		{"expected not a dry run", true, func(r *types.Report) { r.SuiteConfig.DryRun = false }},
		{"different filter", false, func(r *types.Report) { r.SuiteConfig.LabelFilter = "lws" }},
		{"failed suite", false, func(r *types.Report) { r.SuiteSucceeded = false }},
		{"programmatic focus", true, func(r *types.Report) { r.SuiteHasProgrammaticFocus = true }},
		{"focus strings", true, func(r *types.Report) { r.SuiteConfig.FocusStrings = []string{"running"} }},
		{"skip strings", false, func(r *types.Report) { r.SuiteConfig.SkipStrings = []string{"completed"} }},
		{"focus files", false, func(r *types.Report) { r.SuiteConfig.FocusFiles = []string{"jobset_test.go"} }},
		{"skip files", true, func(r *types.Report) { r.SuiteConfig.SkipFiles = []string{"lws_test.go"} }},
		{"setup failed", false, func(r *types.Report) { r.SpecReports[0].State = types.SpecStateFailed }},
		{"missing flow", false, func(r *types.Report) { r.SpecReports = r.SpecReports[:len(r.SpecReports)-1] }},
		{"duplicate flow", false, func(r *types.Report) { r.SpecReports = append(r.SpecReports, r.SpecReports[len(r.SpecReports)-1]) }},
		{"selected flow skipped", false, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].State = types.SpecStateSkipped }},
		{"selected flow pending", true, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].State = types.SpecStatePending }},
		{"selected flow panicked", false, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].State = types.SpecStatePanicked }},
		{"another workload executed", false, func(r *types.Report) { r.SpecReports[1].State = types.SpecStatePassed }},
		{"extra live flow", false, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].LeafNodeText = "unexpected" }},
		{"unsafe flow path", true, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].LeafNodeText = "../running" }},
		{"unknown selected kind", true, func(r *types.Report) {
			r.SpecReports[len(r.SpecReports)-1].ContainerHierarchyTexts = []string{"UnknownJob"}
		}},
		{"missing workload label", true, func(r *types.Report) { r.SpecReports[len(r.SpecReports)-1].ContainerHierarchyLabels = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := writeVerificationFiles(t, "jobset")
			report := flowReport("jobset", test.expected)
			test.change(&report)
			path := files.actual
			if test.expected {
				path = files.expected
			}
			if err := reporters.GenerateJUnitReport(report, path); err != nil {
				t.Fatal(err)
			}
			if err := verify(files.expected, files.actual, files.installed, files.labels, files.root); err == nil {
				t.Fatal("accepted incomplete or unexpected execution")
			}
		})
	}
}

func TestReadReportRejectsMissingKind(t *testing.T) {
	report := flowReport("kserve", true)
	for i, spec := range report.SpecReports {
		if slices.Contains(spec.ContainerHierarchyTexts, "LLMInferenceService") {
			report.SpecReports = append(report.SpecReports[:i], report.SpecReports[i+1:]...)
			break
		}
	}
	path := filepath.Join(t.TempDir(), "expected.xml")
	if err := reporters.GenerateJUnitReport(report, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readReport(path, "kserve", map[string]bool{"kserve": true}, true); err == nil {
		t.Fatal("accepted KServe without its LLMInferenceService cases")
	}
}

func TestReadReportRejectsInvalidStructure(t *testing.T) {
	for _, change := range []string{"missing property", "duplicate property", "incorrect count", "no suite", "multiple suites", "empty suite"} {
		t.Run(change, func(t *testing.T) {
			files := writeVerificationFiles(t, "jobset")
			data, err := os.ReadFile(files.actual)
			if err != nil {
				t.Fatal(err)
			}
			var report reporters.JUnitTestSuites
			if err := xml.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "missing property":
				report.TestSuites[0].Properties.Properties = report.TestSuites[0].Properties.Properties[1:]
			case "duplicate property":
				report.TestSuites[0].Properties.Properties = append(report.TestSuites[0].Properties.Properties, report.TestSuites[0].Properties.Properties[0])
			case "incorrect count":
				report.Tests++
			case "no suite":
				report.TestSuites = nil
			case "multiple suites":
				report.TestSuites = append(report.TestSuites, report.TestSuites[0])
			case "empty suite":
				report.Tests, report.Disabled = 0, 0
				report.TestSuites[0].Tests, report.TestSuites[0].Skipped = 0, 0
				report.TestSuites[0].TestCases = nil
			}
			data, err = xml.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(files.actual, data, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := readReport(files.actual, "jobset", map[string]bool{"jobset": true}, false); err == nil {
				t.Fatal("accepted malformed JUnit report")
			}
		})
	}
}

func TestVerifyRejectsWrongRecordings(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*recorder.Recording)
	}{
		{"schema version", func(r *recorder.Recording) { r.SchemaVersion++ }},
		{"Kubernetes fallback version", func(r *recorder.Recording) { r.Version = "v1.34.0" }},
		{"operator", func(r *recorder.Recording) { r.Operator = "lws" }},
		{"flow", func(r *recorder.Recording) { r.Flow = "completed" }},
		{"Karta", func(r *recorder.Recording) { r.KartaName = "wrong" }},
		{"catalog path", func(r *recorder.Recording) { r.KartaFile = "../../wrong.yaml" }},
		{"failed result", func(r *recorder.Recording) { r.Result.Succeeded = false }},
		{"failure alongside success", func(r *recorder.Recording) { r.Result.Failures = []string{"cleanup failed"} }},
		{"empty events", func(r *recorder.Recording) { r.Events = nil }},
		{"missing state", func(r *recorder.Recording) { r.Events[0].State = "" }},
		{"wrong kind", func(r *recorder.Recording) { r.Events[0].Object["kind"] = "Other" }},
		{"wrong API version", func(r *recorder.Recording) { r.Events[0].Object["apiVersion"] = "v1" }},
		{"missing object", func(r *recorder.Recording) { r.Events[0].Object = nil }},
		{"unknown event", func(r *recorder.Recording) { r.Events[0].Kind = "UNKNOWN" }},
		{"invalid action", func(r *recorder.Recording) { r.Events = append(r.Events, recorder.Event{Kind: recorder.EventAction}) }},
		{"actions only", func(r *recorder.Recording) {
			r.Events = []recorder.Event{{Kind: recorder.EventAction, Action: &recorder.RecordedAction{Name: "resume", Operation: recorder.Operation{Verb: "patch"}}}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := writeVerificationFiles(t, "jobset")
			path := filepath.Join(files.root, "jobset", "v0.99.0", workloads["JobSet"].karta, "running.yaml")
			reader, err := recorder.OpenRecording(path)
			if err != nil {
				t.Fatal(err)
			}
			recording := reader.Recording()
			test.change(&recording)
			writeRecording(t, path, recording)
			if err := verify(files.expected, files.actual, files.installed, files.labels, files.root); err == nil {
				t.Fatal("accepted invalid recording")
			}
		})
	}
}

func TestVerifyRequiresExactRecordingFiles(t *testing.T) {
	for _, change := range []string{"missing", "extra", "directory", "symlink", "operator symlink", "version symlink", "another karta", "malformed", "unknown field"} {
		t.Run(change, func(t *testing.T) {
			files := writeVerificationFiles(t, "jobset")
			dir := filepath.Join(files.root, "jobset", "v0.99.0", workloads["JobSet"].karta)
			path := filepath.Join(dir, "running.yaml")
			switch change {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "extra", "malformed", "unknown field":
				data := []byte("not: [valid")
				if change == "extra" {
					path = filepath.Join(dir, "extra.yaml")
				}
				if change == "unknown field" {
					var err error
					data, err = os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					data = append(data, []byte("unknownField: value\n")...)
				}
				if err := os.WriteFile(path, data, 0o644); err != nil {
					t.Fatal(err)
				}
			case "directory", "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if change == "directory" {
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(filepath.Join(dir, "new-flow.yaml"), path); err != nil {
					t.Fatal(err)
				}
			case "operator symlink", "version symlink":
				old := filepath.Dir(dir)
				if change == "operator symlink" {
					old = filepath.Dir(old)
				}
				moved := filepath.Join(t.TempDir(), "moved")
				if err := os.Rename(old, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, old); err != nil {
					t.Fatal(err)
				}
			case "another karta":
				if err := os.Mkdir(filepath.Join(filepath.Dir(dir), "another-karta"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := verify(files.expected, files.actual, files.installed, files.labels, files.root); err == nil {
				t.Fatal("accepted unexpected recording files")
			}
		})
	}
}

func TestReadVersionsRejectsMissingOrUnsafeMetadata(t *testing.T) {
	for _, contents := range []string{"", "lws=v1.0.0\n", "jobset=\n", "jobset=../elsewhere\n", "jobset=v1.0.0\njobset=v1.0.0\n", "jobset=v1.0.0\nmalformed\n"} {
		t.Run(contents, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "installed")
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := readVersions(path, map[string]bool{"jobset": true}); err == nil {
				t.Fatal("accepted missing or unsafe installed version")
			}
		})
	}
}

func TestVerifyRejectsUnknownGroups(t *testing.T) {
	for _, labels := range []string{"", "unknown", "jobset,jobset", "jobset,", "jobset || lws", "../jobset"} {
		if err := verify("", "", "", labels, ""); err == nil || !strings.Contains(err.Error(), "workload group") {
			t.Fatalf("labels %q: want workload group error, got %v", labels, err)
		}
	}
}

func flowReport(labels string, dryRun bool) types.Report {
	report := types.Report{
		SuiteDescription: "Karta E2E Flows",
		SuiteSucceeded:   true,
		SuiteConfig:      types.SuiteConfig{LabelFilter: labels, DryRun: dryRun},
		SpecReports: types.SpecReports{
			{LeafNodeType: types.NodeTypeBeforeSuite, State: types.SpecStatePassed},
			{LeafNodeType: types.NodeTypeIt, LeafNodeText: "running", ContainerHierarchyTexts: []string{"Pod (built-in)"}, ContainerHierarchyLabels: [][]string{{"pod", "builtin"}}, State: types.SpecStateSkipped},
		},
	}
	var descriptions []string
	for description := range workloads {
		descriptions = append(descriptions, description)
	}
	slices.Sort(descriptions)
	for _, description := range descriptions {
		workload := workloads[description]
		state := types.SpecStateSkipped
		if slices.Contains(strings.Split(labels, ","), workload.operator) {
			state = types.SpecStatePassed
		}
		report.SpecReports = append(report.SpecReports, types.SpecReport{
			LeafNodeType:             types.NodeTypeIt,
			LeafNodeText:             "running",
			ContainerHierarchyTexts:  []string{description},
			ContainerHierarchyLabels: [][]string{{workload.operator}},
			State:                    state,
		})
	}
	// A newly added spec must require a recording without a hardcoded count.
	if slices.Contains(strings.Split(labels, ","), "jobset") {
		report.SpecReports = append(report.SpecReports, types.SpecReport{
			LeafNodeType:             types.NodeTypeIt,
			LeafNodeText:             "new-flow",
			ContainerHierarchyTexts:  []string{"JobSet"},
			ContainerHierarchyLabels: [][]string{{"jobset"}},
			State:                    types.SpecStatePassed,
		})
	}
	return report
}

func writeVerificationFiles(t *testing.T, labels string) verificationFiles {
	t.Helper()
	dir := t.TempDir()
	files := verificationFiles{
		expected: filepath.Join(dir, "expected.xml"), actual: filepath.Join(dir, "actual.xml"),
		installed: filepath.Join(dir, "installed"), root: filepath.Join(dir, "recordings"), labels: labels,
	}
	if err := reporters.GenerateJUnitReport(flowReport(labels, true), files.expected); err != nil {
		t.Fatal(err)
	}
	if err := reporters.GenerateJUnitReport(flowReport(labels, false), files.actual); err != nil {
		t.Fatal(err)
	}
	selected := make(map[string]bool)
	versions := make(map[string]string)
	var installed strings.Builder
	for _, group := range strings.Split(labels, ",") {
		selected[group] = true
		version := "v0.99.0"
		if group == "kubeflow" {
			version = "v1.9.0+mpiv0.8.2"
		}
		versions[group] = version
		installed.WriteString(group + "=" + version + "\n")
	}
	if err := os.WriteFile(files.installed, []byte(installed.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	flows, err := readReport(files.expected, labels, selected, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range flows {
		recording := recorder.Recording{
			SchemaVersion: 1, Operator: workload.operator, Version: versions[workload.operator],
			KartaName: workload.karta, KartaFile: "docs/catalog/" + workload.karta + ".yaml", Flow: workload.flow,
			Result: recorder.Result{Succeeded: true},
			Events: []recorder.Event{{Kind: recorder.EventState, State: "Running", Object: map[string]any{"apiVersion": workload.apiVersion, "kind": workload.kind}}},
		}
		path := filepath.Join(files.root, workload.operator, recording.Version, workload.karta, workload.flow+".yaml")
		writeRecording(t, path, recording)
	}
	return files
}

func writeRecording(t *testing.T, path string, recording recorder.Recording) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(recording)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
