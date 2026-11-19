# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
"""Generate hack/loadtest/grafana/dashboard.json. Run: python3 -I gen_dashboard.py <out>.json"""
import json
import sys

DS = {"type": "prometheus", "uid": "prometheus"}
NS = 'namespace=~"$ns"'
JOB = 'job="$job"'
CTR = 'namespace="karta-system",container="$job"'

# Validated categorical slots (dark), fixed per entity. Status colors only on thresholds.
BLUE, ORANGE, AQUA, YELLOW, MAGENTA = "#3987e5", "#d95926", "#199e70", "#c98500", "#d55181"
VIOLET, RED, GRAY = "#9085e9", "#e66767", "#8e8e8e"
GOOD, WARN, CRIT = "#0ca30c", "#fab219", "#d03b3b"
KIND_COLORS = {"Deployment": BLUE, "Job": ORANGE, "JobSet": AQUA, "LeaderWorkerSet": YELLOW, "Pod": MAGENTA}
PHASE_COLORS = {"Running": BLUE, "Initializing": ORANGE, "Completed": AQUA, "Degraded": YELLOW,
                "Suspended": MAGENTA, "Resuming": VIOLET, "Failed": RED, "Pending": ORANGE, "Succeeded": AQUA}

panels = []
y = 0


def color_override(name, color, dashed=False):
    props = [{"id": "color", "value": {"mode": "fixed", "fixedColor": color}}]
    if dashed:
        props += [{"id": "custom.lineStyle", "value": {"fill": "dash", "dash": [8, 6]}},
                  {"id": "custom.fillOpacity", "value": 0}]
    return {"matcher": {"id": "byName", "options": name}, "properties": props}


def row(title):
    global y
    panels.append({"type": "row", "title": title, "collapsed": False, "gridPos": {"x": 0, "y": y, "w": 24, "h": 1}, "panels": []})
    y += 1


def stat(x, w, title, desc, expr, unit="short", steps=None, decimals=None):
    defaults = {"unit": unit, "color": {"mode": "thresholds"},
                "thresholds": {"mode": "absolute", "steps": steps or [{"color": BLUE, "value": None}]}}
    if decimals is not None:
        defaults["decimals"] = decimals
    panels.append({
        "type": "stat", "title": title, "description": desc, "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": w, "h": 4},
        "targets": [{"refId": "A", "expr": expr}],
        "fieldConfig": {"defaults": defaults, "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                    "colorMode": "value", "graphMode": "area", "textMode": "value", "justifyMode": "center",
                    "orientation": "auto", "wideLayout": True},
    })


def ts(x, w, title, desc, targets, unit="short", overrides=(), stack=False, thresholds=None, h=8, decimals=None):
    custom = {"lineWidth": 2, "fillOpacity": 25 if stack else 6, "gradientMode": "opacity", "showPoints": "never",
              "spanNulls": True, "axisSoftMin": 0, "axisBorderShow": False, "drawStyle": "line",
              "stacking": {"mode": "normal" if stack else "none", "group": "A"}}
    defaults = {"unit": unit, "custom": custom, "color": {"mode": "palette-classic"}}
    if decimals is not None:
        defaults["decimals"] = decimals
    if thresholds:
        defaults["thresholds"] = {"mode": "absolute", "steps": thresholds}
        custom["thresholdsStyle"] = {"mode": "line+area"}
    panels.append({
        "type": "timeseries", "title": title, "description": desc, "datasource": DS,
        "gridPos": {"x": x, "y": y, "w": w, "h": h},
        "targets": [{"refId": chr(65 + i), "expr": e, "legendFormat": l} for i, (l, e) in enumerate(targets)],
        "fieldConfig": {"defaults": defaults, "overrides": list(overrides)},
        "options": {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                    "tooltip": {"mode": "multi", "sort": "desc"}},
    })


def by_kind():
    return [color_override(k, c) for k, c in KIND_COLORS.items()]


def by_phase():
    return [color_override(k, c) for k, c in PHASE_COLORS.items()]


pods = f"count(kube_pod_info{{{NS}}})"
rows = f"count(dsx_workload_pod_info{{{NS}}})"

# KPI tiles
stat(0, 3, "Load Pods", "Pods in the load namespaces, from kube-state-metrics. The reference count.", pods)
stat(3, 3, "Exporter Pod rows", "dsx_workload_pod_info series in the load namespaces: Pods the exporter attributes to a workload.", rows)
stat(6, 3, "Coverage", "Exporter Pod rows / load Pods. 100% means every Pod has a row. Short dips during create and churn are normal.",
     f"{rows} / {pods} * 100", unit="percent", decimals=1,
     steps=[{"color": CRIT, "value": None}, {"color": WARN, "value": 95}, {"color": GOOD, "value": 99.5}])
stat(9, 3, "Workloads tracked", "Distinct workload roots with dsx_workload_status rows.",
     f"count(count by (workload_uid) (dsx_workload_status{{{NS}}}))")
stat(12, 3, "Exporter memory", "Working set of the exporter container (cAdvisor).",
     f"max(container_memory_working_set_bytes{{{CTR}}})", unit="bytes")
stat(15, 3, "Scrape duration", "Time Prometheus needs to collect /metrics from the exporter. Yellow from 10s (50k budget), red at 25s (scrape timeout).",
     f"max(scrape_duration_seconds{{{JOB}}})", unit="s", decimals=1,
     steps=[{"color": GOOD, "value": None}, {"color": WARN, "value": 10}, {"color": CRIT, "value": 25}])
stat(18, 3, "Queue depth", "Items waiting in all exporter workqueues. Should return to 0 after each change.",
     f"sum(workqueue_depth{{{JOB}}})",
     steps=[{"color": GOOD, "value": None}, {"color": WARN, "value": 100}, {"color": CRIT, "value": 5000}])
stat(21, 3, "API requests/s", "Kubernetes API requests the exporter sends, all verbs.",
     f"sum(rate(rest_client_requests_total{{{JOB}}}[2m]))", unit="reqps", decimals=1)
y += 4

row("Coverage: does the exporter see every Pod?")
ts(0, 12, "Load Pods vs exporter Pod rows",
   "Dashed: Pods that exist. Solid: Pods the exporter reports. The lines should overlap; a gap below is lag or misses, a gap above is stale rows.",
   [("Load Pods (kube-state-metrics)", pods), ("Exporter Pod rows", rows)],
   overrides=[color_override("Load Pods (kube-state-metrics)", GRAY, dashed=True), color_override("Exporter Pod rows", BLUE)])
ts(12, 12, "Missing and stale rows",
   "Missing: Pods without a row. Stale: rows without a Pod. Both should be 0 in steady state.",
   [("Missing rows", f"clamp_min({pods} - {rows}, 0)"), ("Stale rows", f"clamp_min({rows} - {pods}, 0)")],
   overrides=[color_override("Missing rows", ORANGE), color_override("Stale rows", RED)])
y += 8

row("Workloads")
ts(0, 8, "Workloads by kind",
   "Distinct workload roots the exporter tracks, by kind.",
   [("{{workload_kind}}", f"count by (workload_kind) (max by (workload_kind, workload_uid) (dsx_workload_status{{{NS}}}))")],
   overrides=by_kind(), stack=True)
ts(8, 8, "Workloads by phase",
   "Workloads whose dsx_workload_status is 1 for the phase. A workload can be in two phases, for example Running and Degraded.",
   [("{{phase}}", f"sum by (phase) (dsx_workload_status{{{NS}}})")],
   overrides=by_phase(), stack=True)
ts(16, 8, "Pods by workload kind",
   "Exporter Pod rows by the kind of the workload root they belong to.",
   [("{{workload_kind}}", f"count by (workload_kind) (dsx_workload_pod_info{{{NS}}})")],
   overrides=by_kind(), stack=True)
y += 8
ts(0, 12, "Workload objects in the cluster",
   "Objects the API server stores, all namespaces. jobs.batch also counts the Jobs that JobSets create.",
   [("{{resource}}", 'max by (resource) (apiserver_storage_objects{resource=~"deployments.apps|replicasets.apps|statefulsets.apps|jobs.batch|jobsets.jobset.x-k8s.io|leaderworkersets.leaderworkerset.x-k8s.io"})')])
ts(12, 6, "Owner resolution",
   "Exporter Pod rows by how their owner chain resolved.",
   [("{{owner_resolution}}", f"count by (owner_resolution) (dsx_workload_pod_info{{{NS}}})")])
ts(18, 6, "Invalid observations",
   "Rows the exporter keeps from cache after a failed or expired verification (observation_valid=false). Should be 0.",
   [("Pod rows", f'count(dsx_workload_pod_info{{{NS},observation_valid="false"}}) or vector(0)'),
    ("Workload rows", f'count(dsx_workload_status{{{NS},observation_valid="false"}}) or vector(0)')],
   overrides=[color_override("Pod rows", ORANGE), color_override("Workload rows", VIOLET)])
y += 8

row("Exporter resources")
ts(0, 8, "Memory", "Dashed: the container memory limit.",
   [("Working set", f"max(container_memory_working_set_bytes{{{CTR}}})"),
    ("Go heap in use", f"max(go_memstats_heap_inuse_bytes{{{JOB}}})"),
    ("Limit", f'max(kube_pod_container_resource_limits{{{CTR},resource="memory"}})')],
   unit="bytes", overrides=[color_override("Working set", BLUE), color_override("Go heap in use", AQUA), color_override("Limit", GRAY, dashed=True)])
ts(8, 8, "CPU", "Cores used. Dashed: the container CPU limit.",
   [("Used", f"sum(rate(container_cpu_usage_seconds_total{{{CTR}}}[2m]))"),
    ("Limit", f'max(kube_pod_container_resource_limits{{{CTR},resource="cpu"}})')],
   unit="none", decimals=2, overrides=[color_override("Used", BLUE), color_override("Limit", GRAY, dashed=True)])
ts(16, 8, "Goroutines", "Live goroutines in the exporter process.",
   [("Goroutines", f"max(go_goroutines{{{JOB}}})")], overrides=[color_override("Goroutines", BLUE)])
y += 8

row("Scrape: /metrics")
ts(0, 12, "Scrape duration",
   "Time for one Prometheus scrape of /metrics. Bands: yellow from 10s (50k budget), red from 25s (timeout; the scrape fails and series go stale).",
   [("Scrape duration", f"max(scrape_duration_seconds{{{JOB}}})")], unit="s",
   thresholds=[{"color": "transparent", "value": None}, {"color": WARN, "value": 10}, {"color": CRIT, "value": 25}],
   overrides=[color_override("Scrape duration", BLUE)])
ts(12, 12, "Samples per scrape",
   "Series the exporter returns in one scrape. Grows with Pods and with workloads times phases.",
   [("Samples", f"max(scrape_samples_scraped{{{JOB}}})")], overrides=[color_override("Samples", BLUE)])
y += 8

row("Workqueues and Kubernetes API")
queues = [color_override("exporter-pods", BLUE), color_override("exporter-workloads", ORANGE), color_override("exporter-sources", AQUA)]
ts(0, 6, "Queue depth", "Items waiting, per exporter queue.",
   [("{{name}}", f"max by (name) (workqueue_depth{{{JOB}}})")], overrides=queues)
ts(6, 6, "Queue adds/s", "Items added per second, per queue.",
   [("{{name}}", f"sum by (name) (rate(workqueue_adds_total{{{JOB}}}[2m]))")], unit="ops", overrides=queues)
ts(12, 6, "Queue wait p99", "99th percentile time an item waits in the queue before a worker takes it: the exporter's own freshness delay for a change. Empty while no items are queued. Buckets are powers of ten, so values are estimates within a decade.",
   [("{{name}}", f"histogram_quantile(0.99, sum by (le, name) (rate(workqueue_queue_duration_seconds_bucket{{{JOB}}}[2m])))")], unit="s", overrides=queues)
ts(18, 6, "Work duration p99", "99th percentile time to process one item, per queue. Empty while no items are processed.",
   [("{{name}}", f"histogram_quantile(0.99, sum by (le, name) (rate(workqueue_work_duration_seconds_bucket{{{JOB}}}[2m])))")], unit="s", overrides=queues)
y += 8
ts(0, 8, "API requests/s by method", "Kubernetes API requests the exporter sends.",
   [("{{method}}", f"sum by (method) (rate(rest_client_requests_total{{{JOB}}}[2m]))")], unit="reqps")
ts(8, 8, "Client throttle wait p99", "Time GET, LIST and other request/response calls wait in the exporter's apiQPS/apiBurst limiter. Above 0 means the client limit is the bottleneck. Watches are not timed, so the panel is empty while the exporter makes no such calls, as in steady state.",
   [("{{verb}}", f"histogram_quantile(0.99, sum by (le, verb) (rate(rest_client_rate_limiter_duration_seconds_bucket{{{JOB}}}[2m])))")], unit="s")
ts(16, 8, "API latency p99", "99th percentile latency of the exporter's request/response calls (GET, LIST). Watches are not timed, so the panel is empty while the exporter makes no such calls, as in steady state.",
   [("{{verb}}", f"histogram_quantile(0.99, sum by (le, verb) (rate(rest_client_request_duration_seconds_bucket{{{JOB}}}[2m])))")], unit="s")
y += 8

row("Control plane")
ts(0, 8, "Load Pods by phase", "Pods in the load namespaces by phase. Failed rises during churn, when KWOK fails 1% of Running Pods each cycle.",
   [("{{phase}}", f"sum by (phase) (kube_pod_status_phase{{{NS}}})")], overrides=by_phase(), stack=True)
ts(8, 8, "etcd database size", "From the API server (apiserver_storage_size_bytes), so it works when etcd metrics are not reachable.",
   [("etcd db", "max(apiserver_storage_size_bytes)")], unit="bytes", overrides=[color_override("etcd db", BLUE)])
ts(16, 8, "API server memory", "Resident memory of kube-apiserver.",
   [("RSS", 'max(process_resident_memory_bytes{job="apiserver"})')], unit="bytes", overrides=[color_override("RSS", BLUE)])
y += 8

dashboard = {
    "uid": "karta-load", "title": "Karta exporter scale test", "tags": ["karta-load"],
    "description": "DSX workload exporter under KWOK load. Vertical lines mark the run phases.",
    "timezone": "browser", "refresh": "30s", "time": {"from": "now-2h", "to": "now"},
    "schemaVersion": 39, "graphTooltip": 1, "editable": True,
    "templating": {"list": [
        {"type": "constant", "name": "ns", "query": "karta-load-[0-9]+", "hide": 2},
        {"type": "constant", "name": "job", "query": "dsx-workload-exporter", "hide": 2},
    ]},
    "annotations": {"list": [{
        "name": "Phases", "datasource": {"type": "grafana", "uid": "-- Grafana --"}, "enable": True,
        "iconColor": VIOLET, "target": {"type": "tags", "tags": ["karta-load"], "limit": 100, "matchAny": True},
    }]},
    "panels": [dict(p, id=i + 1) for i, p in enumerate(panels)],
}
with open(sys.argv[1], "w") as f:
    json.dump(dashboard, f, indent=1)
    f.write("\n")
