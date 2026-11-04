// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func recordUpdate(ctx context.Context, opts updateOptions, exec executor) (result error) {
	if err := checkArtifactDirectory(opts.Root, opts.Artifacts); err != nil {
		return err
	}
	started, err := os.OpenFile(filepath.Join(opts.Artifacts, "record-started"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("start fresh recording attempt: %w", err)
	}
	if err := started.Close(); err != nil {
		return err
	}
	var plan updatePlan
	if err := readJSONFile(filepath.Join(opts.Artifacts, "plan.json"), &plan); err != nil {
		return err
	}
	candidate, err := prepareCandidate(ctx, opts.Root, plan, opts.BaseSHA)
	if err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(opts.Artifacts, "candidate.json"), candidate); err != nil {
		return err
	}
	for _, path := range candidate.Directories {
		if err := os.RemoveAll(filepath.Join(opts.Root, path)); err != nil {
			return fmt.Errorf("remove stale recordings: %w", err)
		}
	}

	cluster := "renovate-" + opts.Group + "-" + opts.RunID + "-" + opts.RunAttempt
	kubeconfig := filepath.Join(opts.RunnerTemp, "renovate-"+opts.Group+"-kubeconfig")
	env := environment(exec.env)
	for key := range env {
		if strings.HasSuffix(key, "_VERSION") {
			delete(env, key)
		}
	}
	for _, key := range []string{"FLOW", "E2E_FOCUS", "E2E_LABELS", "KIND_NODE_IMAGE"} {
		delete(env, key)
	}
	env["CLUSTER_NAME"], env["KUBECONFIG"] = cluster, kubeconfig
	exec.env = environmentList(env)
	clusters, err := exec.output(ctx, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(string(clusters)) {
		if name == cluster {
			return fmt.Errorf("cluster %s already exists; recording requires a fresh cluster", cluster)
		}
	}
	checkDir := ""
	defer func() {
		// Cancellation must still allow deletion of the temporary cluster.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		if result != nil {
			if err := exec.logged(cleanupCtx, filepath.Join(opts.Artifacts, "kind-export.log"), "kind", "export", "logs", filepath.Join(opts.Artifacts, "kind"), "--name", cluster); err != nil {
				fmt.Fprintf(exec.stderr, "collect diagnostics: %v\n", err)
			}
		}
		cleanupErr := exec.logged(cleanupCtx, filepath.Join(opts.Artifacts, "cleanup.log"), "kind", "delete", "cluster", "--name", cluster)
		if err := os.Remove(kubeconfig); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErr = errors.Join(cleanupErr, err)
		}
		if checkDir != "" {
			cleanupErr = errors.Join(cleanupErr, removeCheckDirectory(checkDir))
		}
		if cleanupErr != nil {
			if result == nil {
				result = fmt.Errorf("clean recording environment: %w", cleanupErr)
			} else {
				fmt.Fprintf(exec.stderr, "clean recording environment: %v\n", cleanupErr)
			}
		}
		if result == nil {
			manifest, err := candidateManifest(ctx, opts.Root, candidate)
			if err == nil {
				err = writeJSONFile(filepath.Join(opts.Artifacts, "manifest.json"), manifest)
			}
			result = err
		}
		if result != nil {
			if err := os.Remove(filepath.Join(opts.Artifacts, "manifest.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
				fmt.Fprintf(exec.stderr, "remove failed recording evidence: %v\n", err)
			}
		}
	}()

	patch, err := gitOutput(ctx, opts.Root, "diff", "--", "hack/e2e/global.env")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.Artifacts, "pins.patch"), patch, 0o644); err != nil {
		return err
	}
	workloads, labels := strings.Join(plan.Workloads, " "), strings.Join(plan.Workloads, ",")
	if err := exec.logged(ctx, filepath.Join(opts.Artifacts, "install.log"), "make", "e2e-up", "WORKLOADS="+workloads, "CLUSTER_NAME="+cluster, "KARTA_WEBHOOK_MODE="+plan.WebhookMode); err != nil {
		return err
	}
	installedPath := filepath.Join(opts.Root, "hack/e2e/operators/.installed-versions-"+cluster)
	if err := verifyInstalled(candidate, plan, installedPath); err != nil {
		return err
	}
	installed, err := os.ReadFile(installedPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(opts.Artifacts, "installed-versions"), installed, 0o644); err != nil {
		return err
	}
	if err := collectClusterEvidence(ctx, exec, opts.Artifacts); err != nil {
		return err
	}
	if plan.WebhookMode == "cert-manager" {
		if err := exec.logged(ctx, filepath.Join(opts.Artifacts, "operator.log"), "make", "test-operator-e2e", "CLUSTER_NAME="+cluster, "KARTA_WEBHOOK_MODE="+plan.WebhookMode); err != nil {
			return err
		}
	}
	flows := exec
	flows.dir = filepath.Join(opts.Root, "test/e2e")
	if err := flows.logged(ctx, filepath.Join(opts.Artifacts, "discovery.log"), "go", "test", "-count=1", "./flows", "-args", "-ginkgo.dry-run", "-ginkgo.label-filter="+labels, "-ginkgo.junit-report="+filepath.Join(opts.Artifacts, "expected.xml")); err != nil {
		return err
	}
	if err := exec.logged(ctx, filepath.Join(opts.Artifacts, "recording.log"), "make", "record-e2e", "WORKLOADS="+workloads, "CLUSTER_NAME="+cluster, "E2E_JUNIT_REPORT="+filepath.Join(opts.Artifacts, "actual.xml")); err != nil {
		return err
	}
	if err := flows.logged(ctx, filepath.Join(opts.Artifacts, "verification.log"), "go", "run", "./cmd/verify-operator-update", "-expected-junit", filepath.Join(opts.Artifacts, "expected.xml"), "-junit", filepath.Join(opts.Artifacts, "actual.xml"), "-installed-versions", filepath.Join(opts.Artifacts, "installed-versions"), "-workloads", labels); err != nil {
		return err
	}
	if err := addRecordingHeaders(opts.Root, candidate); err != nil {
		return err
	}
	if err := exec.logged(ctx, filepath.Join(opts.Artifacts, "replay.log"), "make", "verify-recordings", "test-replay"); err != nil {
		return err
	}

	// Presubmit requires committed files; Renovate creates the published commit.
	checkDir, err = os.MkdirTemp(opts.RunnerTemp, "operator-check-")
	if err != nil {
		return err
	}
	if err := exec.run(ctx, "git", "clone", "--quiet", "--shared", opts.Root, checkDir); err != nil {
		return err
	}
	if err := copyCandidate(opts.Root, checkDir, candidate); err != nil {
		return err
	}
	check := exec
	check.dir = checkDir
	for _, args := range [][]string{
		{"git", "config", "user.name", "CI validation"},
		{"git", "config", "user.email", "ci@example.com"},
		{"git", "add", "--", "hack/e2e/global.env", "test/e2e/recorded_data"},
		{"git", "-c", "commit.gpgsign=false", "commit", "--quiet", "-s", "-m", "test(e2e): validate operator update recordings"},
	} {
		if err := check.run(ctx, args...); err != nil {
			return err
		}
	}
	if err := check.logged(ctx, filepath.Join(opts.Artifacts, "make-check.log"), "make", "check", "VERSION=0.0.0-main-"+opts.BaseSHA[:7]); err != nil {
		return err
	}
	fmt.Fprintf(exec.stdout, "Validated %s in %s\n", opts.Group, opts.RunURL)
	return nil
}

func collectClusterEvidence(ctx context.Context, exec executor, artifacts string) error {
	data, err := exec.output(ctx, "kubectl", "version", "-o", "json")
	if err != nil {
		return err
	}
	var version struct {
		ServerVersion json.RawMessage `json:"serverVersion"`
	}
	if err := json.Unmarshal(data, &version); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(artifacts, "kubernetes.json"), version); err != nil {
		return err
	}
	data, err = exec.output(ctx, "kubectl", "get", "pods", "-A", "-o", "json")
	if err != nil {
		return err
	}
	type container struct {
		Image   string `json:"image"`
		ImageID string `json:"imageID"`
		Ready   bool   `json:"ready"`
	}
	var pods struct {
		Items []struct {
			Metadata struct{ Namespace, Name string }
			Status   struct{ ContainerStatuses []container }
		}
	}
	if err := json.Unmarshal(data, &pods); err != nil {
		return err
	}
	type podImages struct {
		Namespace  string      `json:"namespace"`
		Pod        string      `json:"pod"`
		Containers []container `json:"containers"`
	}
	images := make([]podImages, 0, len(pods.Items))
	for _, pod := range pods.Items {
		images = append(images, podImages{pod.Metadata.Namespace, pod.Metadata.Name, pod.Status.ContainerStatuses})
	}
	return writeJSONFile(filepath.Join(artifacts, "images.json"), images)
}
