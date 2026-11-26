// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

package config

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.yaml.in/yaml/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigsyaml "sigs.k8s.io/yaml"
)

type chartTest interface {
	Helper()
	Skip(...any)
	Fatal(...any)
	Fatalf(string, ...any)
}

func runExporterChart(t chartTest, values string, extra ...string) ([]byte, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is needed for chart rendering")
	}
	args := []string{"template", "install", filepath.Join("..", "..", "..", "charts", "karta"), "--set", "exporter.enabled=true", "-f", "-"}
	cmd := exec.Command(helm, append(args, extra...)...)
	cmd.Stdin = strings.NewReader(values)
	return cmd.CombinedOutput()
}

func renderExporterChart(t chartTest, values string, extra ...string) []byte {
	t.Helper()
	out, err := runExporterChart(t, values, extra...)
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

func exporterConfigFile(t chartTest, manifests []byte) string {
	t.Helper()
	for _, raw := range chartDocuments(t, manifests) {
		var cm corev1.ConfigMap
		if err := sigsyaml.Unmarshal(raw, &cm); err != nil {
			t.Fatal(err)
		}
		if cm.Kind == "ConfigMap" && cm.Name == "dsx-workload-exporter" {
			return cm.Data["config.yaml"]
		}
	}
	t.Fatal("exporter config not found")
	return ""
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
	ginkgo.It("mounts selection, metric, worker, and API client settings as supplied", func() {
		t := ginkgo.GinkgoT()
		values := `exporter:
  selection:
    namespaceSelector: {matchExpressions: [], matchLabels: {observe: "true"}}
    rootSelectors: [{selector: {matchLabels: {team: platform}}}]
    podSelector: {matchLabels: {scrape: "true"}}
    includeRootResources: [{apiGroups: [batch], apiVersions: [v1], resources: [jobs]}]
    excludeRootResources: [{apiGroups: [apps], apiVersions: [v1], resources: [deployments]}]
    ignoredOwnerKinds: [apps/v1/ReplicaSet]
    requiredWatchKinds: [batch/v1/Job]
  metrics:
    workloadSelector: {matchLabels: {report: "true"}}
    podWorkloadAssociation: {target: {apiVersion: batch/v1, kind: Job, nameFromLabel: job-name}}
    excludeStatuses: [Running]
    labelRules: [{name: team, sources: [pod], metrics: [dsx_workload_pod_info], expression: object.metadata.name}]
    labelEvaluationLimits: {costLimit: 500, maxExpressionBytes: 512, maxResultBytes: 32}
  tuning:
    workers: {default: 7, pods: 3, workloads: 2}
    apiClient: {requestTimeout: 15s, qps: 12, burst: 24}
  kartaDefinitionSource:
    enableExperimental: true
    catalog: {enabled: true, include: [batch-job-v1], exclude: []}
    cluster: {enabled: false, include: [team-job], exclude: [other-job]}
`
		rendered := renderExporterChart(t, values, "--set", "exporter.tuning.goMemoryLimit=1Gi", "--set", "exporter.tuning.pprof.enabled=true")
		content := exporterConfigFile(t, rendered)
		var expected struct{ Exporter map[string]any }
		var actual map[string]any
		Expect(yaml.Unmarshal([]byte(values), &expected)).To(Succeed())
		Expect(yaml.Unmarshal([]byte(content), &actual)).To(Succeed())
		Expect(actual).To(Equal(expected.Exporter))
		_, err := loadConfig(content, nil)
		Expect(err).NotTo(HaveOccurred())
	})

	ginkgo.It("preserves chart defaults and explicit empty lists and zero values", func() {
		t := ginkgo.GinkgoT()
		values := `exporter:
  selection:
    excludeRootResources: []
  metrics:
    excludeStatuses: []
    labelEvaluationLimits: {maxResultBytes: 0}
  tuning:
    workers: {default: 7, pods: 0}
`
		content := exporterConfigFile(t, renderExporterChart(t, values, "--set", "exporter.selection.namespaceSelector.matchLabels.team=platform"))
		cfg, err := loadConfig(content, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Selection.NamespaceSelector).To(Equal(&metav1.LabelSelector{
			MatchLabels: map[string]string{"team": "platform"},
			MatchExpressions: []metav1.LabelSelectorRequirement{{
				Key: "kubernetes.io/metadata.name", Operator: metav1.LabelSelectorOpNotIn,
				Values: []string{"kube-system", "kube-public", "kube-node-lease"},
			}},
		}))
		Expect(cfg.Selection.ExcludeRootResources).To(BeEmpty())
		Expect(cfg.Metrics.ExcludeStatuses).To(BeEmpty())
		Expect(cfg.Metrics.Limits.MaxResultBytes).To(BeZero())
		Expect(cfg.Metrics.Limits.CostLimit).To(Equal(defaultLabelEvaluationLimits.CostLimit))
		Expect(cfg.Tuning.Workers.Default).To(Equal(7))
		Expect(cfg.Tuning.Workers.Pods).To(BeZero())
		Expect(cfg.Tuning.APIClient.RequestTimeout).To(Equal("30s"))
	})

	ginkgo.DescribeTable("rejects unknown exporter keys during Helm rendering",
		func(key, value string) {
			t := ginkgo.GinkgoT()
			out, err := runExporterChart(t, "", "--set", "exporter.selection.namespaceSelector.matchLabels.team=platform",
				"--set-json", "exporter."+key+"="+value)
			Expect(err).To(HaveOccurred())
			Expect(string(out)).To(ContainSubstring(key))
			Expect(string(out)).To(ContainSubstring("not allowed"))
		},
		ginkgo.Entry("namespace selector outside selection", "namespaceSelector", `{"matchLabels":{"team":"training"}}`),
		ginkgo.Entry("object selectors outside selection", "objectSelectors", `[{"selector":{"matchLabels":{"team":"training"}}}]`),
		ginkgo.Entry("misspelled group", "selectoin", "{}"),
	)

	ginkgo.DescribeTable("rejects invalid group shapes during Helm rendering",
		func(override, message string) {
			t := ginkgo.GinkgoT()
			out, err := runExporterChart(t, "", "--set-json", override)
			Expect(err).To(HaveOccurred())
			Expect(string(out)).To(ContainSubstring(message))
		},
		ginkgo.Entry("worker count in place of a group", "exporter.tuning.workers=3", "exporter.tuning.workers must be a map"),
		ginkgo.Entry("list in place of a group", "exporter.tuning.apiClient=[]", "exporter.tuning.apiClient must be a map"),
		ginkgo.Entry("null selection", "exporter.selection=null", "exporter.selection must be a map"),
	)

	ginkgo.DescribeTable("rejects unknown fields when loading the rendered config",
		func(override, field string) {
			t := ginkgo.GinkgoT()
			out := renderExporterChart(t, "", "--set-json", override)
			_, err := loadConfig(exporterConfigFile(t, out), nil)
			Expect(err).To(MatchError(ContainSubstring(`unknown field "` + field + `"`)))
		},
		ginkgo.Entry("unknown selection key", "exporter.selection.namespaceSelecter={}", "namespaceSelecter"),
		ginkgo.Entry("misplaced worker setting", "exporter.metrics.workers={}", "workers"),
		ginkgo.Entry("unknown API client setting", "exporter.tuning.apiClient.qsp=12", "qsp"),
	)
})

var _ = ginkgo.Describe("chart exporter serving TLS", func() {
	exporterDeployment := func(t chartTest, out []byte) appsv1.Deployment {
		t.Helper()
		for _, raw := range chartDocuments(t, out) {
			var deployment appsv1.Deployment
			Expect(sigsyaml.Unmarshal(raw, &deployment)).To(Succeed())
			if deployment.Kind == "Deployment" && deployment.Name == "dsx-workload-exporter" {
				return deployment
			}
		}
		t.Fatal("exporter Deployment not found")
		return appsv1.Deployment{}
	}

	ginkgo.It("mounts the Secret, probes over HTTPS and loads the mounted pair", func() {
		t := ginkgo.GinkgoT()
		out := renderExporterChart(t, "", "--set", "exporter.metrics.delivery.prometheus.tls.existingSecret=dsx-exporter-tls")
		pod := exporterDeployment(t, out).Spec.Template.Spec
		Expect(pod.Volumes).To(ContainElement(corev1.Volume{
			Name:         "tls",
			VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "dsx-exporter-tls"}},
		}))
		container := pod.Containers[0]
		Expect(container.VolumeMounts).To(ContainElement(corev1.VolumeMount{Name: "tls", MountPath: "/etc/dsx-workload-exporter-tls", ReadOnly: true}))
		Expect(container.ReadinessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS))
		Expect(container.LivenessProbe.HTTPGet.Scheme).To(Equal(corev1.URISchemeHTTPS))

		cfg, err := loadConfig(exporterConfigFile(t, out), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.Prometheus.TLS).To(Equal(ServingTLSConfig{
			CertFile: "/etc/dsx-workload-exporter-tls/tls.crt",
			KeyFile:  "/etc/dsx-workload-exporter-tls/tls.key",
		}))
	})

	ginkgo.It("keeps plain HTTP by default", func() {
		t := ginkgo.GinkgoT()
		out := renderExporterChart(t, "")
		pod := exporterDeployment(t, out).Spec.Template.Spec
		Expect(pod.Volumes).To(HaveLen(1))
		Expect(pod.Containers[0].ReadinessProbe.HTTPGet.Scheme).To(BeEmpty())
		Expect(pod.Containers[0].LivenessProbe.HTTPGet.Scheme).To(BeEmpty())
		Expect(exporterConfigFile(t, out)).NotTo(ContainSubstring("delivery"))
	})

	ginkgo.DescribeTable("rejects TLS settings the chart cannot apply",
		func(message string, args ...string) {
			out, err := runExporterChart(ginkgo.GinkgoT(), "", args...)
			Expect(err).To(HaveOccurred())
			Expect(string(out)).To(ContainSubstring(message))
		},
		ginkgo.Entry("ServiceMonitor TLS without a serving certificate",
			"exporter.serviceMonitor.tlsConfig requires exporter.metrics.delivery.prometheus.tls.existingSecret",
			"--set", "exporter.serviceMonitor.enabled=true", "--set", "exporter.serviceMonitor.tlsConfig.serverName=exporter"),
		ginkgo.Entry("file paths in place of a Secret", "certFile",
			"--set", "exporter.metrics.delivery.prometheus.tls.certFile=/tls/tls.crt"),
	)
})

var _ = ginkgo.Describe("chart exporter OTLP delivery", func() {
	exporterPod := func(t chartTest, out []byte) corev1.PodSpec {
		t.Helper()
		for _, raw := range chartDocuments(t, out) {
			var deployment appsv1.Deployment
			Expect(sigsyaml.Unmarshal(raw, &deployment)).To(Succeed())
			if deployment.Kind == "Deployment" && deployment.Name == "dsx-workload-exporter" {
				return deployment.Spec.Template.Spec
			}
		}
		t.Fatal("exporter Deployment not found")
		return corev1.PodSpec{}
	}

	ginkgo.It("loads the example from the issue", func() {
		t := ginkgo.GinkgoT()
		out := renderExporterChart(t, `exporter:
  metrics:
    delivery:
      mode: otlp
      otlp:
        protocol: http/protobuf
        endpoint: https://collector.example.com:4318/v1/metrics
        interval: 30s
        timeout: 10s
`)
		cfg, err := loadConfig(exporterConfigFile(t, out), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.Mode).To(Equal(DeliveryOTLP))
		Expect(cfg.Metrics.Delivery.OTLP).To(Equal(OTLPDeliveryConfig{
			Protocol: ProtocolHTTPProtobuf, Endpoint: "https://collector.example.com:4318/v1/metrics", Interval: "30s", Timeout: "10s",
		}))
		pod := exporterPod(t, out)
		Expect(pod.Volumes).To(HaveLen(1))
		Expect(pod.Containers[0].ReadinessProbe.HTTPGet.Path).To(Equal("/readyz"))
	})

	ginkgo.It("reads headers from a Secret and mounts the CA and client certificate", func() {
		t := ginkgo.GinkgoT()
		out := renderExporterChart(t, `exporter:
  metrics:
    delivery:
      mode: otlp
      otlp:
        protocol: grpc
        endpoint: https://collector.observability.svc:4317
        headers:
          secretKeyRef: {name: otlp-auth, key: headers}
        tls:
          ca:
            configMapKeyRef: {name: otlp-ca, key: bundle.pem}
          clientSecret: otlp-client
`)
		pod := exporterPod(t, out)
		container := pod.Containers[0]
		Expect(container.Env).To(ContainElement(corev1.EnvVar{
			Name: "OTEL_EXPORTER_OTLP_METRICS_HEADERS",
			ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "otlp-auth"}, Key: "headers",
			}},
		}))
		Expect(pod.Volumes).To(ContainElements(
			corev1.Volume{Name: "otlp-ca", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "otlp-ca"},
				Items:                []corev1.KeyToPath{{Key: "bundle.pem", Path: "ca.crt"}},
			}}},
			corev1.Volume{Name: "otlp-client", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "otlp-client"}}},
		))
		Expect(container.VolumeMounts).To(ContainElements(
			corev1.VolumeMount{Name: "otlp-ca", MountPath: "/etc/dsx-workload-exporter-otlp/ca", ReadOnly: true},
			corev1.VolumeMount{Name: "otlp-client", MountPath: "/etc/dsx-workload-exporter-otlp/client", ReadOnly: true},
		))

		cfg, err := loadConfig(exporterConfigFile(t, out), nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(cfg.Metrics.Delivery.OTLP.TLS).To(Equal(OTLPTLSConfig{
			CAFile:   "/etc/dsx-workload-exporter-otlp/ca/ca.crt",
			CertFile: "/etc/dsx-workload-exporter-otlp/client/tls.crt",
			KeyFile:  "/etc/dsx-workload-exporter-otlp/client/tls.key",
		}))
	})

	ginkgo.It("takes the CA from a Secret key", func() {
		t := ginkgo.GinkgoT()
		out := renderExporterChart(t, "", "--set", "exporter.metrics.delivery.mode=otlp",
			"--set", "exporter.metrics.delivery.otlp.endpoint=https://collector:4318/v1/metrics",
			"--set", "exporter.metrics.delivery.otlp.tls.ca.secretKeyRef.name=collector-ca",
			"--set", "exporter.metrics.delivery.otlp.tls.ca.secretKeyRef.key=ca.crt")
		Expect(exporterPod(t, out).Volumes).To(ContainElement(corev1.Volume{Name: "otlp-ca", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
			SecretName: "collector-ca", Items: []corev1.KeyToPath{{Key: "ca.crt", Path: "ca.crt"}},
		}}}))
	})

	ginkgo.DescribeTable("rejects settings the exporter cannot push with",
		func(message string, args ...string) {
			out, err := runExporterChart(ginkgo.GinkgoT(), "", args...)
			Expect(err).To(HaveOccurred())
			Expect(string(out)).To(ContainSubstring(message))
		},
		ginkgo.Entry("no endpoint", "exporter.metrics.delivery.otlp.endpoint is required in otlp mode",
			"--set", "exporter.metrics.delivery.mode=otlp"),
		ginkgo.Entry("a serving certificate in otlp mode", "exporter.metrics.delivery.prometheus.tls.existingSecret applies only in prometheus mode",
			"--set", "exporter.metrics.delivery.mode=otlp", "--set", "exporter.metrics.delivery.otlp.endpoint=https://c:4318/v1/metrics",
			"--set", "exporter.metrics.delivery.prometheus.tls.existingSecret=dsx-exporter-tls"),
		ginkgo.Entry("an unknown protocol", "exporter.metrics.delivery.otlp.protocol must be one of the following",
			"--set", "exporter.metrics.delivery.otlp.protocol=http/json"),
		ginkgo.Entry("an interval that is not a duration", "exporter.metrics.delivery.otlp.interval",
			"--set", "exporter.metrics.delivery.otlp.interval=soon"),
		ginkgo.Entry("a header Secret without its key", "exporter.metrics.delivery.otlp.headers.secretKeyRef: key is required",
			"--set", "exporter.metrics.delivery.otlp.headers.secretKeyRef.name=otlp-auth"),
		ginkgo.Entry("two CA sources", "exporter.metrics.delivery.otlp.tls.ca: Must have at most 1 properties",
			"--set", "exporter.metrics.delivery.otlp.tls.ca.configMapKeyRef.name=a", "--set", "exporter.metrics.delivery.otlp.tls.ca.configMapKeyRef.key=b",
			"--set", "exporter.metrics.delivery.otlp.tls.ca.secretKeyRef.name=c", "--set", "exporter.metrics.delivery.otlp.tls.ca.secretKeyRef.key=d"),
	)
})
