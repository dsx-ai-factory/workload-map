// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

// Command verify-operator-update checks that every selected flow ran and produced
// a successful recording under the installed operator version.
package main

import (
	"bufio"
	"encoding/xml"
	"flag"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/onsi/ginkgo/v2/reporters"
	"sigs.k8s.io/yaml"

	"github.com/dsx-ai-factory/workload-map/test/e2e/recorder"
)

type fixture struct {
	operator   string
	karta      string
	apiVersion string
	kind       string
	flow       string
}

// Only the workload-to-catalog mapping is fixed. Flow names come from a dry run
// of the same suite, so adding an It requires a recording without changing a count.
var workloads = map[string]fixture{
	"LeaderWorkerSet":         {"lws", "leaderworkerset-x-k8s-io-leaderworkerset-v1", "leaderworkerset.x-k8s.io/v1", "LeaderWorkerSet", ""},
	"JobSet":                  {"jobset", "jobset-x-k8s-io-jobset-v1alpha2", "jobset.x-k8s.io/v1alpha2", "JobSet", ""},
	"RayCluster":              {"kuberay", "ray-io-raycluster-v1", "ray.io/v1", "RayCluster", ""},
	"RayJob":                  {"kuberay", "ray-io-rayjob-v1", "ray.io/v1", "RayJob", ""},
	"PyTorchJob":              {"kubeflow", "kubeflow-org-pytorchjob-v1", "kubeflow.org/v1", "PyTorchJob", ""},
	"MPIJob":                  {"kubeflow", "kubeflow-org-mpijob-v2beta1", "kubeflow.org/v2beta1", "MPIJob", ""},
	"KnativeService":          {"knative", "serving-knative-dev-service-v1", "serving.knative.dev/v1", "Service", ""},
	"KServe InferenceService": {"kserve", "serving-kserve-io-inferenceservice-v1beta1", "serving.kserve.io/v1beta1", "InferenceService", ""},
	"LLMInferenceService":     {"kserve", "serving-kserve-io-llminferenceservice-v1alpha2", "serving.kserve.io/v1alpha2", "LLMInferenceService", ""},
	"Milvus":                  {"milvus", "milvus-io-milvus-v1beta1", "milvus.io/v1beta1", "Milvus", ""},
	"Grove PodCliqueSet":      {"grove", "grove-io-podcliqueset-v1alpha1", "grove.io/v1alpha1", "PodCliqueSet", ""},
	"DynamoGraphDeployment":   {"dynamo", "nvidia-com-dynamographdeployment-v1alpha1", "nvidia.com/v1alpha1", "DynamoGraphDeployment", ""},
	"NIMService":              {"nim", "apps-nvidia-com-nimservice-v1alpha1", "apps.nvidia.com/v1alpha1", "NIMService", ""},
	"Sandbox":                 {"agent-sandbox", "agents-x-k8s-io-sandbox-v1beta1", "agents.x-k8s.io/v1beta1", "Sandbox", ""},
}

var (
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)
	flowPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func main() {
	expected := flag.String("expected-junit", "", "Ginkgo dry-run JUnit report that enumerates the selected flows")
	actual := flag.String("junit", "", "Ginkgo JUnit report from the live recording run")
	installed := flag.String("installed-versions", "", "per-cluster installed operator versions file")
	labels := flag.String("workloads", "", "comma-separated operator groups, matching both reports' label filter")
	root := flag.String("recorded-root", "recorded_data", "recorded fixture root")
	flag.Parse()
	if flag.NArg() != 0 || *expected == "" || *actual == "" || *installed == "" || *labels == "" {
		flag.Usage()
		os.Exit(2)
	}
	if err := verify(*expected, *actual, *installed, *labels, *root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("verified all selected flows and recordings for %s\n", *labels)
}

func verify(expectedPath, actualPath, installedPath, labels, root string) error {
	selected := make(map[string]bool)
	for _, group := range strings.Split(labels, ",") {
		known := false
		for _, workload := range workloads {
			known = known || workload.operator == group
		}
		if !known || selected[group] {
			return fmt.Errorf("unknown or duplicate workload group %q", group)
		}
		selected[group] = true
	}
	expected, err := readReport(expectedPath, labels, selected, true)
	if err != nil {
		return fmt.Errorf("expected report: %w", err)
	}
	actual, err := readReport(actualPath, labels, selected, false)
	if err != nil {
		return fmt.Errorf("recording report: %w", err)
	}
	if !maps.Equal(expected, actual) {
		return fmt.Errorf("live flow set differs from the dry run: expected %d flows, got %d", len(expected), len(actual))
	}
	versions, err := readVersions(installedPath, selected)
	if err != nil {
		return err
	}
	return verifyRecordings(root, expected, versions)
}

func readReport(path, labels string, selected map[string]bool, dryRun bool) (map[string]fixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read JUnit: %w", err)
	}
	var report reporters.JUnitTestSuites
	if err := xml.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("parse JUnit: %w", err)
	}
	if len(report.TestSuites) != 1 || report.Errors != 0 || report.Failures != 0 {
		return nil, fmt.Errorf("expected one successful Ginkgo suite")
	}
	suite := report.TestSuites[0]
	if suite.Name != "Karta E2E Flows" || suite.Errors != 0 || suite.Failures != 0 || suite.Disabled != 0 ||
		report.Tests != len(suite.TestCases) || suite.Tests != len(suite.TestCases) || report.Disabled != suite.Skipped {
		return nil, fmt.Errorf("unexpected suite identity, results, or counters")
	}
	for name, want := range map[string]string{
		"SuiteSucceeded":            "true",
		"SuiteHasProgrammaticFocus": "false",
		"SpecialSuiteFailureReason": "",
		"DryRun":                    strconv.FormatBool(dryRun),
		"LabelFilter":               labels,
		"FocusStrings":              "",
		"SkipStrings":               "",
		"FocusFiles":                "",
		"SkipFiles":                 "",
		"SemVerFilter":              "",
	} {
		count := 0
		for _, property := range suite.Properties.Properties {
			if property.Name == name {
				count++
				if property.Value != want {
					return nil, fmt.Errorf("JUnit property %s = %q, want %q", name, property.Value, want)
				}
			}
		}
		if count != 1 {
			return nil, fmt.Errorf("expected one JUnit property %s, got %d", name, count)
		}
	}

	flows := make(map[string]fixture)
	seen := make(map[string]bool)
	kinds := make(map[string]bool)
	skipped := 0
	for _, test := range suite.TestCases {
		if test.Classname != suite.Name || test.Error != nil || test.Failure != nil || seen[test.Name] {
			return nil, fmt.Errorf("unexpected, duplicate, or failed JUnit case %q", test.Name)
		}
		seen[test.Name] = true
		if !strings.HasPrefix(test.Name, "[It] ") {
			if test.Status != "passed" || test.Skipped != nil {
				return nil, fmt.Errorf("suite setup or cleanup did not pass: %q", test.Name)
			}
			continue
		}
		name, labelText, ok := strings.Cut(strings.TrimPrefix(test.Name, "[It] "), " [")
		if !ok || !strings.HasSuffix(labelText, "]") {
			return nil, fmt.Errorf("missing workload labels on %q", test.Name)
		}
		isSelected := false
		for _, label := range strings.Split(strings.TrimSuffix(labelText, "]"), ",") {
			isSelected = isSelected || selected[strings.TrimSpace(label)]
		}
		if !isSelected {
			if test.Status != "skipped" || test.Skipped == nil || test.Skipped.Message != "skipped" {
				return nil, fmt.Errorf("unexpected execution outside selected workloads: %q", test.Name)
			}
			skipped++
			continue
		}
		if test.Status != "passed" || test.Skipped != nil {
			return nil, fmt.Errorf("selected flow did not pass: %q", test.Name)
		}
		found := false
		for description, workload := range workloads {
			flow, matches := strings.CutPrefix(name, description+" ")
			if !matches || !selected[workload.operator] {
				continue
			}
			if !flowPattern.MatchString(flow) {
				return nil, fmt.Errorf("invalid recording flow name %q", flow)
			}
			workload.flow = flow
			flows[test.Name] = workload
			kinds[description] = true
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("unknown selected workload in %q", test.Name)
		}
	}
	if skipped != suite.Skipped || len(flows) == 0 {
		return nil, fmt.Errorf("inconsistent skipped count or no selected flows")
	}
	for description, workload := range workloads {
		if selected[workload.operator] && !kinds[description] {
			return nil, fmt.Errorf("no flows for selected workload %s", description)
		}
	}
	return flows, nil
}

func readVersions(path string, selected map[string]bool) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read installed versions: %w", err)
	}
	versions := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		name, version, ok := strings.Cut(scanner.Text(), "=")
		if !ok || !flowPattern.MatchString(name) || !versionPattern.MatchString(version) || versions[name] != "" {
			return nil, fmt.Errorf("invalid or duplicate installed version entry %q", scanner.Text())
		}
		versions[name] = version
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read installed version entry: %w", err)
	}
	for group := range selected {
		if versions[group] == "" {
			return nil, fmt.Errorf("missing installed version for %s", group)
		}
	}
	return versions, nil
}

func verifyRecordings(root string, flows map[string]fixture, versions map[string]string) error {
	expected := make(map[string]fixture)
	directories := make(map[string]bool)
	versionDirs := make(map[string]bool)
	for _, workload := range flows {
		versionDir := filepath.Join(root, workload.operator, versions[workload.operator])
		kindDir := filepath.Join(versionDir, workload.karta)
		path := filepath.Join(kindDir, workload.flow+".yaml")
		if _, duplicate := expected[path]; duplicate {
			return fmt.Errorf("multiple flow cases write %s", path)
		}
		expected[path] = workload
		directories[kindDir] = true
		versionDirs[versionDir] = true
	}
	recordings := 0
	for versionDir := range versionDirs {
		// WalkDir does not follow child symlinks. Check its ancestors too, so a
		// symlinked operator directory cannot import recordings from another run.
		for _, dir := range []string{root, filepath.Dir(versionDir), versionDir} {
			info, err := os.Lstat(dir)
			if err != nil {
				return fmt.Errorf("inspect recording directory: %w", err)
			}
			if !info.IsDir() {
				return fmt.Errorf("expected a real recording directory: %s", dir)
			}
		}
		err := filepath.WalkDir(versionDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() && (path == versionDir || directories[path]) {
				return nil
			}
			workload, ok := expected[path]
			if !ok || !entry.Type().IsRegular() {
				return fmt.Errorf("unexpected recording file or directory: %s", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			var recording recorder.Recording
			if err := yaml.UnmarshalStrict(data, &recording); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			if recording.SchemaVersion != 1 || recording.Operator != workload.operator ||
				recording.Version != versions[workload.operator] || recording.Flow != workload.flow ||
				recording.KartaName != workload.karta || recording.KartaFile != "docs/catalog/"+workload.karta+".yaml" {
				return fmt.Errorf("unexpected recording metadata in %s", path)
			}
			if !recording.Result.Succeeded || len(recording.Result.Failures) != 0 {
				return fmt.Errorf("recorded flow did not succeed: %s", path)
			}
			states := 0
			for _, event := range recording.Events {
				switch event.Kind {
				case recorder.EventState:
					if event.State == "" || event.Object["kind"] != workload.kind || event.Object["apiVersion"] != workload.apiVersion {
						return fmt.Errorf("invalid STATE event in %s", path)
					}
					states++
				case recorder.EventAction:
					if event.Action == nil || event.Action.Name == "" || event.Action.Operation.Verb == "" {
						return fmt.Errorf("invalid ACTION event in %s", path)
					}
				default:
					return fmt.Errorf("unknown event kind %q in %s", event.Kind, path)
				}
			}
			if states == 0 {
				return fmt.Errorf("no STATE events in %s", path)
			}
			recordings++
			return nil
		})
		if err != nil {
			return fmt.Errorf("verify recordings: %w", err)
		}
	}
	if recordings != len(expected) {
		return fmt.Errorf("expected %d recordings, found %d", len(expected), recordings)
	}
	return nil
}
