#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
set -euo pipefail

if [[ ${1:-} == --help && $# == 1 ]]; then
  cat <<'EOF'
Usage: ./demo-grafana-exporter.sh

Create a fresh Kind cluster with the current DSX exporter, fake GPU operator,
Prometheus, Grafana, and example workloads. Requires Docker, Kind, kubectl,
Helm, Make, Go (with automatic toolchain downloads), Git, curl, tar, and shasum.
GPU readings are simulated; CPU and memory readings come from kubelet cAdvisor.

Environment:
  CLUSTER_NAME     Cluster to create (default: grafana-exporter-demo)
  GRAFANA_PORT     Local Grafana port (default: 23000)
  PROMETHEUS_PORT  Local Prometheus port (default: 29090)

Build files and a dedicated kubeconfig are kept in a temporary directory.
An existing cluster with the same name is refused. Ctrl-C stops port forwards
and leaves the cluster available for inspection. Cleanup commands are printed.
EOF
  exit 0
fi
if [[ $# != 0 ]]; then
  echo 'Usage: ./demo-grafana-exporter.sh [--help]' >&2
  exit 1
fi

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
assets="$repo_dir/hack/demo-grafana-exporter"
cluster_name=${CLUSTER_NAME:-grafana-exporter-demo}
grafana_port=${GRAFANA_PORT:-23000}
prometheus_port=${PROMETHEUS_PORT:-29090}
if [[ ! $cluster_name =~ ^[a-z0-9]+(-[a-z0-9]+)*$ ]]; then
  echo 'CLUSTER_NAME must contain lowercase letters, digits, and separated hyphens.' >&2
  exit 1
fi
for port in "$grafana_port" "$prometheus_port"; do
  if [[ ! $port =~ ^[1-9][0-9]{0,4}$ ]] || (( port > 65535 )); then
    echo "Invalid local port: $port" >&2
    exit 1
  fi
done
if [[ $grafana_port == "$prometheus_port" ]]; then
  echo 'Grafana and Prometheus need different local ports.' >&2
  exit 1
fi
for tool in docker kind kubectl helm make go git curl tar shasum; do
  command -v "$tool" >/dev/null || { echo "Required command not found: $tool" >&2; exit 1; }
done
architecture=$(docker info --format '{{.Architecture}}')
case "$architecture" in
  x86_64|amd64) architecture=amd64 ;;
  aarch64|arm64) architecture=arm64 ;;
  *) echo "Unsupported Docker architecture: $architecture" >&2; exit 1 ;;
esac
clusters=$(kind get clusters)
while IFS= read -r existing; do
  if [[ $existing == "$cluster_name" ]]; then
    echo "Cluster $cluster_name already exists. Choose another CLUSTER_NAME or delete it first." >&2
    exit 1
  fi
done <<< "$clusters"

umask 077
work_dir=$(mktemp -d /tmp/grafana-exporter-demo.XXXXXX)
image_tag="demo-${work_dir##*.}"
export KUBECONFIG="$work_dir/kubeconfig"
export GOWORK=off GOFLAGS=-mod=readonly
forward_pids=()
cleanup() {
  local pid
  if (( ${#forward_pids[@]} > 0 )); then
    for pid in "${forward_pids[@]}"; do kill "$pid" 2>/dev/null || true; done
    for pid in "${forward_pids[@]}"; do wait "$pid" 2>/dev/null || true; done
  fi
  printf '\nBuild files and kubeconfig: %s\n' "$work_dir"
  printf 'Inspect: kubectl --kubeconfig %q get pods -A\n' "$KUBECONFIG"
  printf 'Delete cluster: kind delete cluster --name %q --kubeconfig %q\n' "$cluster_name" "$KUBECONFIG"
  printf 'Then remove build files: rm -rf %q\n' "$work_dir"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'Build directory: %s\nGPU measurements in this demo are simulated.\n' "$work_dir"

echo 'Building the current DSX exporter...'
GOARCH="$architecture" make -C "$repo_dir" exporter-image \
  EXPORTER_IMAGE_REPOSITORY=local/dsx-workload-exporter IMAGE_TAG="$image_tag" \
  BUILD_ARGS="--platform linux/$architecture"

echo 'Building fake GPU operator v0.0.72 with the Pod UID patch...'
curl --fail --location --retry 3 \
  https://github.com/run-ai/fake-gpu-operator/archive/refs/tags/v0.0.72.tar.gz \
  -o "$work_dir/fake-gpu.tar.gz"
(
  cd "$work_dir"
  printf '%s  fake-gpu.tar.gz\n' 363de8c2987c11307c988391572786263054687969f1f8d2548beedea775f0a8 \
    | shasum -a 256 --check
  tar -xzf fake-gpu.tar.gz
  cd fake-gpu-operator-0.0.72
  git apply "$assets/fake-gpu-pod-uid.patch"
  GOTOOLCHAIN=go1.25.4 go test ./internal/status-exporter/export/metrics ./internal/status-updater/handlers/pod
  mkdir -p bin
  GOTOOLCHAIN=go1.25.4 CGO_ENABLED=0 GOOS=linux GOARCH="$architecture" \
    go build -trimpath -o bin/ ./cmd/status-updater ./cmd/status-exporter ./cmd/status-exporter-kwok
)
mkdir -p "$work_dir/fake-image"
cp -R "$work_dir/fake-gpu-operator-0.0.72/bin" "$work_dir/fake-image/bin"
cp "$work_dir/fake-gpu-operator-0.0.72/LICENSE" "$work_dir/fake-image/LICENSE"
for component in status-updater status-exporter; do
  docker build --platform "linux/$architecture" --target "$component" \
    -t "local/fake-gpu-$component:$image_tag" \
    -f "$assets/Dockerfile.fake-gpu" "$work_dir/fake-image"
done

kind create cluster --name "$cluster_name" --image kindest/node:v1.34.0 \
  --config "$assets/kind.yaml" --kubeconfig "$KUBECONFIG" --wait 180s
kind load docker-image --name "$cluster_name" "local/dsx-workload-exporter:$image_tag" \
  "local/fake-gpu-status-updater:$image_tag" "local/fake-gpu-status-exporter:$image_tag"
kubectl label nodes -l '!node-role.kubernetes.io/control-plane' run.ai/simulated-gpu-node-pool=default
helm install fake-gpu-operator oci://ghcr.io/run-ai/fake-gpu-operator/fake-gpu-operator \
  --version 0.0.72 -n dashboard-gpu --create-namespace -f "$assets/fake-gpu-values.yaml" \
  --set-string statusUpdater.image.tag="$image_tag",statusExporter.image.tag="$image_tag" --wait --timeout 5m
kubectl -n dashboard-gpu rollout status deployment/status-updater --timeout=180s
kubectl -n dashboard-gpu rollout status daemonset/device-plugin --timeout=180s
kubectl -n dashboard-gpu rollout status daemonset/nvidia-dcgm-exporter --timeout=180s
kubectl wait --for=jsonpath='{.status.allocatable.nvidia\.com/gpu}'=8 \
  node -l run.ai/simulated-gpu-node-pool=default --timeout=180s
helm install dsx "$repo_dir/charts/karta" -n dashboard-lab --create-namespace \
  --skip-crds -f "$assets/exporter-values.yaml" --set-string exporter.image.tag="$image_tag" --wait --timeout 5m

for dashboard in workload-overview workload-structure; do
  kubectl -n dashboard-lab create configmap "dashboard-$dashboard" \
    --from-file="$dashboard.json=$repo_dir/dsx-workload-exporter/examples/grafana/$dashboard.json" \
    --dry-run=client -o yaml | kubectl apply --server-side --field-manager=grafana-demo -f -
done
kubectl apply -f "$assets/monitoring.yaml"
kubectl apply -f "$assets/workloads.yaml"
for deployment in dsx-workload-exporter prometheus grafana gpu-active cpu-only; do
  kubectl -n dashboard-lab rollout status "deployment/$deployment" --timeout=180s
done
kubectl -n dashboard-lab wait --for=condition=Ready pod/gpu-quiet --timeout=180s
kubectl -n dashboard-lab wait --for=condition=Complete job/completed --timeout=180s
kubectl -n dashboard-lab wait --for=condition=Failed job/failed --timeout=180s

kubectl -n dashboard-lab port-forward --address=127.0.0.1 svc/grafana "$grafana_port:3000" >"$work_dir/grafana.log" 2>&1 &
forward_pids+=("$!")
kubectl -n dashboard-lab port-forward --address=127.0.0.1 svc/prometheus "$prometheus_port:9090" >"$work_dir/prometheus.log" 2>&1 &
forward_pids+=("$!")
deadline=$((SECONDS + 60))
until curl --fail --silent --max-time 2 "http://127.0.0.1:$grafana_port/api/health" >/dev/null \
  && curl --fail --silent --max-time 2 "http://127.0.0.1:$prometheus_port/-/ready" >/dev/null; do
  for pid in "${forward_pids[@]}"; do
    kill -0 "$pid" 2>/dev/null || { cat "$work_dir/grafana.log" "$work_dir/prometheus.log" >&2; exit 1; }
  done
  if (( SECONDS >= deadline )); then echo 'Port forwards did not become ready.' >&2; exit 1; fi
  sleep 1
done
cat <<EOF

Overview: http://127.0.0.1:$grafana_port/d/dsx-example-workloads
Structure: http://127.0.0.1:$grafana_port/d/dsx-example-structure
Prometheus: http://127.0.0.1:$prometheus_port

Select Kind Prometheus, jobs lab-identities / lab-gpu / lab-containers,
and DCGM namespace label namespace. Leave Cluster blank.
GPU readings are simulated: gpu-active is 80%, gpu-quiet is 2%.
Allow about three minutes of scrapes for gpu-quiet to become Low activity.
Ctrl-C stops port forwarding and leaves the cluster running.
EOF
while :; do
  for pid in "${forward_pids[@]}"; do
    kill -0 "$pid" 2>/dev/null || { cat "$work_dir/grafana.log" "$work_dir/prometheus.log" >&2; exit 1; }
  done
  sleep 2
done
