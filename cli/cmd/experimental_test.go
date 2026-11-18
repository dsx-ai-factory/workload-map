// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/api/runai/v1alpha1"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog"
	"github.com/dsx-ai-factory/workload-map/karta/sdk/pkg/catalog/kartas"
)

func TestDefinitionsExperimentalOptIn(t *testing.T) {
	isolateConfig(t)
	t.Setenv("KARTA_CATALOG_EXPERIMENTAL_DEFINITIONS", "")
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "absent"))
	cluster := kartas.Jobset()
	cluster.Name = "experimental-cluster-jobset"
	cluster.Spec.Experimental = true
	body, err := json.Marshal(v1alpha1.KartaList{
		TypeMeta: metav1.TypeMeta{APIVersion: "run.ai/v1alpha1", Kind: "KartaList"},
		Items:    []v1alpha1.Karta{*cluster},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	for _, format := range []string{"table", "json", "yaml"} {
		for _, enabled := range []bool{false, true} {
			args := []string{"--server", server.URL, "-o", format}
			if enabled {
				args = append(args, "--catalog-experimental-definitions")
			}
			out, errOut, code := runCmd(t, "definitions", args...)
			if code != 0 || errOut != "" {
				t.Fatalf("%s, enabled=%t: exit %d, stderr %s", format, enabled, code, errOut)
			}
			for _, name := range []string{cluster.Name, kartas.KueueWorkload().Name} {
				if strings.Contains(out, name) != enabled {
					t.Errorf("%s, enabled=%t: unexpected visibility for %s", format, enabled, name)
				}
			}
			fallback := kartas.Jobset().Name
			if format != "table" {
				if strings.Contains(out, fallback) == enabled {
					t.Errorf("%s, enabled=%t: stable catalog fallback has wrong visibility", format, enabled)
				}
				continue
			}
			// The table keeps the catalog definition the cluster one overrides,
			// marked inactive, so only its status tells the two cases apart.
			want := "active"
			if enabled {
				want = "inactive"
			}
			var status string
			for _, line := range strings.Split(out, "\n") {
				if fields := strings.Fields(line); len(fields) > 3 && fields[0] == fallback {
					status = fields[3]
				}
			}
			if status != want {
				t.Errorf("table, enabled=%t: stable catalog fallback is %q, want %q", enabled, status, want)
			}
		}
	}

	for _, name := range []string{cluster.Name, kartas.KueueWorkload().Name} {
		if out, _, code := runCmd(t, "definitions", "--server", server.URL, name); code == 0 || out != "" {
			t.Errorf("explicit name %s bypassed opt-in: exit %d, stdout %s", name, code, out)
		}
		if _, errOut, code := runCmd(t, "definitions", "--server", server.URL, name, "--catalog-experimental-definitions"); code != 0 {
			t.Errorf("enabled explicit name %s: exit %d, stderr %s", name, code, errOut)
		}
	}
}

func TestExperimentalWorkloadCommands(t *testing.T) {
	isolateConfig(t)
	t.Setenv("KARTA_CATALOG_EXPERIMENTAL_DEFINITIONS", "")
	gvk := catalog.RootKey(kartas.KueueWorkload())
	gvr := gvk.GroupVersion().WithResource("workloads")
	workload := object(gvk, "queued-training", nil)
	client, _ := fakeMixedCluster(t, workload)

	for _, tc := range []struct {
		command string
		args    []string
	}{
		{"get", []string{"workload"}},
		{"get", []string{"workloads/queued-training"}},
		{"describe", []string{"workload/queued-training"}},
		{"get", []string{"--all-types"}},
	} {
		for _, enabled := range []bool{false, true} {
			client.ClearActions()
			args := slices.Clone(tc.args)
			if enabled {
				args = append(args, "--catalog-experimental-definitions")
			}
			out, errOut, code := runCmd(t, tc.command, args...)
			if enabled && code != 0 {
				t.Errorf("%s %v: exit %d, stderr %s", tc.command, args, code, errOut)
			}
			if strings.Contains(out, workload.GetName()) != enabled {
				t.Errorf("%s %v: unexpected workload visibility: %s", tc.command, args, out)
			}
			if tc.args[0] == "--all-types" && strings.Contains(errOut, gvk.Group) {
				t.Errorf("discovery announced excluded workload: %s", errOut)
			}
			if !enabled {
				for _, action := range client.Actions() {
					if action.GetResource() == gvr {
						t.Errorf("%s %v queried an excluded workload", tc.command, args)
					}
				}
			}
		}
	}
}

func TestExperimentalManifestOptIn(t *testing.T) {
	isolateConfig(t)
	t.Setenv("KARTA_CATALOG_EXPERIMENTAL_DEFINITIONS", "")
	noCluster(t)
	// Another version exercises the fallback through ByRootKind too.
	for _, version := range []string{"v1beta2", "v1beta1"} {
		path := writeManifest(t, "apiVersion: kueue.x-k8s.io/"+version+"\nkind: Workload\nmetadata:\n  name: queued-training\n")
		if out, _, code := runDescribeCmd(t, "-f", path); code == 0 || out != "" {
			t.Errorf("%s manifest bypassed opt-in: exit %d, stdout %s", version, code, out)
		}
		out, errOut, code := runDescribeCmd(t, "-f", path, "--catalog-experimental-definitions")
		if code != 0 || !strings.Contains(out, "queued-training") {
			t.Errorf("%s enabled manifest: exit %d, stdout %s, stderr %s", version, code, out, errOut)
		}
	}
}

func TestExperimentalCompletionPreferences(t *testing.T) {
	isolateConfig(t)
	t.Setenv("KARTA_CATALOG_EXPERIMENTAL_DEFINITIONS", "")
	fakeCluster(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("catalog-experimental-definitions: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		env  string
		args []string
		want bool
	}{
		{"default", "", nil, false},
		{"flag", "", []string{"--catalog-experimental-definitions"}, true},
		{"config", "", []string{"--config", path}, true},
		{"environment", "true", nil, true},
		{"environment overrides config", "false", []string{"--config", path}, false},
		{"explicit false", "true", []string{"--config", path, "--catalog-experimental-definitions=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("KARTA_CATALOG_EXPERIMENTAL_DEFINITIONS", tc.env)
			args := append([]string{"get"}, tc.args...)
			got := complete(t, append(args, "work")...)
			if slices.Contains(got, "workload") != tc.want {
				t.Errorf("workload visibility=%t, completions %v", tc.want, got)
			}
		})
	}
}
