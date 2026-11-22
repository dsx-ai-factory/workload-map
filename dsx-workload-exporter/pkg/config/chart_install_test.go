// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"go.yaml.in/yaml/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

type chartTest interface {
	Helper()
	Skip(...any)
	Fatal(...any)
	Fatalf(string, ...any)
}

func renderExporterChart(t chartTest, values string, extra ...string) []byte {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is needed for chart rendering")
	}
	args := []string{"template", "install", filepath.Join("..", "..", "..", "charts", "karta"), "--set", "exporter.enabled=true", "-f", "-"}
	cmd := exec.Command(helm, append(args, extra...)...)
	cmd.Stdin = strings.NewReader(values)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	return out
}

func chartDocuments(t chartTest, out []byte) [][]byte {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	var docs [][]byte
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return docs
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := yaml.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, raw)
	}
}

func TestExporterInstallationValues(t *testing.T) {
	values := `exporter:
  metricsPort: 9090
  resources:
    requests:
      cpu: 25m
      memory: 32Mi
  nodeSelector:
    kubernetes.io/os: linux
  tolerations:
    - key: dedicated
      operator: Exists
  affinity:
    nodeAffinity:
      preferredDuringSchedulingIgnoredDuringExecution:
        - weight: 1
          preference:
            matchExpressions:
              - key: pool
                operator: In
                values: [observability]
  podLabels:
    team: platform
    app.kubernetes.io/name: forbidden
  podAnnotations:
    example.com/owner: platform
    checksum/config: forbidden
  service:
    annotations:
      example.com/scrape: "true"
`
	for _, override := range []string{"", "custom"} {
		t.Run("override="+override, func(t *testing.T) {
			name := "dsx-workload-exporter"
			if override != "" {
				name = override + "-" + name
			}
			out := renderExporterChart(t, values, "--set", "fullnameOverride="+override)
			var deployment appsv1.Deployment
			var service corev1.Service
			for _, raw := range chartDocuments(t, out) {
				var meta struct {
					Kind     string
					Metadata struct{ Name string }
				}
				if err := sigsyaml.Unmarshal(raw, &meta); err != nil {
					t.Fatal(err)
				}
				if meta.Metadata.Name != name {
					continue
				}
				switch meta.Kind {
				case "Deployment":
					if err := sigsyaml.Unmarshal(raw, &deployment); err != nil {
						t.Fatal(err)
					}
				case "Service":
					if err := sigsyaml.Unmarshal(raw, &service); err != nil {
						t.Fatal(err)
					}
				}
			}
			if deployment.Name == "" || service.Name == "" {
				t.Fatal("exporter Deployment or Service missing")
			}
			pod := deployment.Spec.Template
			if pod.Labels["team"] != "platform" || pod.Labels["app.kubernetes.io/name"] != "dsx-workload-exporter" {
				t.Fatalf("unexpected pod labels: %v", pod.Labels)
			}
			for key, value := range deployment.Spec.Selector.MatchLabels {
				if pod.Labels[key] != value || service.Spec.Selector[key] != value {
					t.Fatalf("selector mismatch for %s", key)
				}
			}
			if pod.Annotations["example.com/owner"] != "platform" || len(pod.Annotations["checksum/config"]) != 64 {
				t.Fatalf("unexpected pod annotations: %v", pod.Annotations)
			}
			if pod.Spec.NodeSelector["kubernetes.io/os"] != "linux" || len(pod.Spec.Tolerations) != 1 || pod.Spec.Tolerations[0].Key != "dedicated" || pod.Spec.Affinity == nil || pod.Spec.Affinity.NodeAffinity == nil {
				t.Fatal("scheduling values missing")
			}
			container := pod.Spec.Containers[0]
			if container.Resources.Requests.Cpu().String() != "25m" || container.Resources.Requests.Memory().String() != "32Mi" {
				t.Fatalf("resources: %v", container.Resources)
			}
			if container.Ports[0].ContainerPort != 9090 || !strings.Contains(strings.Join(container.Args, " "), "--metrics-bind-address=:9090") || service.Spec.Ports[0].Port != 9090 || service.Spec.Ports[0].TargetPort.String() != "metrics" {
				t.Fatal("listener and Service ports must agree")
			}
			if service.Annotations["example.com/scrape"] != "true" || !service.Spec.PublishNotReadyAddresses {
				t.Fatal("Service annotations or diagnostic access missing")
			}
			if container.ReadinessProbe.HTTPGet.Port.String() != "metrics" || container.LivenessProbe.HTTPGet.Port.String() != "metrics" {
				t.Fatal("probes must use listener port")
			}
		})
	}
}

func TestExporterDisabledIsolation(t *testing.T) {
	baseline := renderExporterChart(t, "", "--set", "exporter.enabled=false")
	changed := renderExporterChart(t, `exporter:
  metricsPort: 9090
  resources: {requests: {cpu: 25m}}
  nodeSelector: {pool: exporter}
  podLabels: {team: exporter}
  podAnnotations: {owner: exporter}
  service: {annotations: {owner: exporter}}
`, "--set", "exporter.enabled=false")
	if !bytes.Equal(baseline, changed) {
		t.Fatal("disabled exporter settings changed other chart resources")
	}
	if bytes.Contains(baseline, []byte("dsx-workload-exporter")) {
		t.Fatal("disabled exporter resources rendered")
	}
}

var _ = ginkgo.Describe("chart runtime configuration", func() {
	ginkgo.It("loads overridden runtime settings", func() {
		t := ginkgo.GinkgoT()
		values := `exporter:
  namespaceSelector: {matchExpressions: [], matchLabels: {observe: "true"}}
  objectSelectors: [{selector: {matchLabels: {team: platform}}}]
  podSelector: {matchLabels: {scrape: "true"}}
  resourceRules: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]
  excludeResourceRules: [{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}]
  ignoredOwnerKinds: [apps/v1/ReplicaSet]
  requiredKinds: [batch/v1/Job]
  workers: 7
  leafWorkers: 3
  rootWorkers: 2
  requestTimeout: 15s
  apiQPS: 12
  apiBurst: 24
  kartaDefinitionSource:
    catalog: {enabled: true, include: [batch-job-v1], exclude: []}
    cluster: {enabled: false, include: [team-job], exclude: [other-job]}
`
		out := renderExporterChart(t, values)
		for _, raw := range chartDocuments(t, out) {
			var cm corev1.ConfigMap
			if err := sigsyaml.Unmarshal(raw, &cm); err != nil {
				t.Fatal(err)
			}
			if cm.Kind != "ConfigMap" || cm.Name != "dsx-workload-exporter" {
				continue
			}
			cfg, err := loadConfig(cm.Data["config.yaml"], nil)
			if err != nil {
				t.Fatal(err)
			}
			var input struct {
				Exporter map[string]any `yaml:"exporter"`
			}
			if err := yaml.Unmarshal([]byte(values), &input); err != nil {
				t.Fatal(err)
			}
			actualJSON, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			var actual map[string]any
			if err := json.Unmarshal(actualJSON, &actual); err != nil {
				t.Fatal(err)
			}
			for key, expected := range input.Exporter {
				want, err := json.Marshal(expected)
				if err != nil {
					t.Fatal(err)
				}
				got, err := json.Marshal(actual[key])
				if err != nil {
					t.Fatal(err)
				}
				// Empty fields may be omitted by the runtime's JSON tags.
				if key == "kartaDefinitionSource" || key == "namespaceSelector" {
					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: got %s, want %s", key, got, want)
				}
			}
			if cfg.NamespaceSelector.MatchLabels["observe"] != "true" || !cfg.KartaDefinitionSource.Catalog.Enabled || cfg.KartaDefinitionSource.Cluster.Enabled || len(cfg.KartaDefinitionSource.Catalog.Include) != 1 || len(cfg.KartaDefinitionSource.Cluster.Exclude) != 1 {
				t.Fatal("definition source or namespace selector not propagated")
			}
			return
		}
		t.Fatal("exporter config not found")

	})
})
