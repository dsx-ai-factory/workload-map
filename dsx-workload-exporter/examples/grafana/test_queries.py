# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

"""Evaluate shipped dashboard queries with synthetic series using promtool."""

import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


DIRECTORY = Path(__file__).resolve().parent
PROMETHEUS_IMAGE = "quay.io/prometheus/prometheus:v3.14.0"
QUERY_NAMES = {}
VARIABLES = {
    "cluster": "test",
    "exporter_job": "dsx",
    "cadvisor_job": "cadvisor",
    "dcgm_job": "dcgm",
    "namespace": "workloads",
    "workload": "training",
    "workload_uid": "workload-1",
    "component": ".*",
    "component_instance": ".*",
    "gpu_device": ".*",
    "activity_view": "4",
    "__range": "10m",
    "__rate_interval": "5m",
}
WORKLOAD = {
    "cluster": "test",
    "namespace": "workloads",
    "workload_name": "training",
    "workload_uid": "workload-1",
}
POD = {
    **WORKLOAD,
    "job": "dsx",
    "pod_name": "worker",
    "pod_uid": "pod-1",
    "observation_valid": "true",
}
REASONS = {
    "active": "measured_above_5pct",
    "low_activity": "reported_devices_quiet_3m",
    "warming": "collecting_quiet_window",
    "unknown": "missing_or_conflicting_telemetry",
}


def labels(values):
    pairs = (f"{key}={json.dumps(value)}" for key, value in sorted(values.items()))
    return "{" + ",".join(pairs) + "}"


def series(metric, values, **identity):
    return {"series": metric + labels(identity), "values": values}


def query(dashboard, panel_id, ref="A", namespace_label="namespace"):
    document = json.loads((DIRECTORY / f"workload-{dashboard}.json").read_text())
    panel = next(panel for panel in document["panels"] if panel["id"] == panel_id)
    expression = next(target["expr"] for target in panel["targets"] if target["refId"] == ref)
    variables = {**VARIABLES, "dcgm_namespace_label": namespace_label}
    # Match named Grafana variables only; PromQL label replacements use literal $1.
    expression = re.sub(
        r"\$(?:\{([A-Za-z_]\w*)(?::\w+)?\}|([A-Za-z_]\w*))",
        lambda match: variables[match[1] or match[2]],
        expression,
    )
    QUERY_NAMES[expression] = f"workload-{dashboard}/{panel_id}/{ref} ({namespace_label})"
    return expression


def expectation(expression, time, value=None, **identity):
    samples = [] if value is None else [{"labels": labels(identity), "value": value}]
    return {"expr": expression, "eval_time": time, "exp_samples": samples}


def state_expectation(expression, time, state):
    return expectation(
        expression, time, 1, **WORKLOAD, state=state, reason=REASONS[state],
        workload="training", workload_key="test/workloads/workload-1",
    )


def fixtures():
    tests = []
    for namespace_label in ("namespace", "exported_namespace"):
        state_query = query("overview", 306, namespace_label=namespace_label)
        gpu = {
            "cluster": "test", "job": "dcgm", namespace_label: "workloads",
            "pod_uid": "pod-1", "UUID": "GPU-1",
        }
        if namespace_label == "exported_namespace":
            gpu["namespace"] = "monitoring"
        attribution = series("dsx_workload_pod_info", "1+0x30", **POD)
        tests.append({
            "name": f"GPU active, warming, low and stale: {namespace_label}",
            "input_series": [
                attribution,
                series("DCGM_FI_DEV_GPU_UTIL", "10 0+0x7 _x20", **gpu),
            ],
            "promql_expr_test": [
                state_expectation(state_query, "0s", "active"),
                state_expectation(state_query, "30s", "warming"),
                state_expectation(state_query, "4m", "low_activity"),
                state_expectation(state_query, "6m", "unknown"),
            ],
        })
        tests.append({
            "name": f"Stale MIG evidence does not hide a fresh full GPU: {namespace_label}",
            "input_series": [
                attribution,
                series("DCGM_FI_DEV_GPU_UTIL", "_x8 10+0x12", **gpu),
                *[
                    series(metric, "1 _x30", **gpu, GPU_I_ID="7", GPU_CI_ID="0")
                    for metric in (
                        "DCGM_FI_DEV_GPU_UTIL", "DCGM_FI_PROF_GR_ENGINE_ACTIVE",
                        "DCGM_FI_DEV_FB_USED",
                    )
                ],
            ],
            "promql_expr_test": [state_expectation(state_query, "4m", "active")],
        })
        tests.append({
            "name": f"A missing second GPU prevents low activity: {namespace_label}",
            "input_series": [
                attribution,
                series("DCGM_FI_DEV_GPU_UTIL", "0+0x30", **gpu),
                series("DCGM_FI_DEV_GPU_UTIL", "0+0x2 _x28", **{**gpu, "UUID": "GPU-2"}),
            ],
            "promql_expr_test": [state_expectation(state_query, "4m", "unknown")],
        })

    for dashboard, panel, metric, values, expected in (
        ("overview", 104, "container_cpu_usage_seconds_total", "0+30x20", 1000),
        ("overview", 105, "container_memory_working_set_bytes", "4096+0x20", 4096),
        ("structure", 11, "container_cpu_usage_seconds_total", "0+30x20", 1000),
        ("structure", 12, "container_memory_working_set_bytes", "4096+0x20", 4096),
    ):
        expression = query(dashboard, panel)
        result_labels = WORKLOAD if dashboard == "structure" else {}
        container = {
            "cluster": "test", "job": "cadvisor", "namespace": "workloads",
            "pod": "worker", "container": "main", "id": "first",
        }
        valid = [
            series("dsx_workload_pod_info", "1+0x20", **POD),
            series(metric, values, **container, instance="replica-a"),
            series(metric, values, **container, instance="replica-b"),
        ]
        tests.append({
            "name": f"Agreeing replicas of one container ID: {dashboard}/{panel}",
            "input_series": valid,
            "promql_expr_test": [expectation(expression, "5m", expected, **result_labels)],
        })
        tests.append({
            "name": f"A conflicting second container ID rejects the container: {dashboard}/{panel}",
            "input_series": valid + [
                series(metric, values, **{**container, "id": "second"}, instance="replica-a"),
                series(metric, "0+60x20", **{**container, "id": "second"}, instance="replica-b"),
            ],
            "promql_expr_test": [expectation(expression, "5m")],
        })

    for dashboard, panel in (("overview", 4), ("structure", 2)):
        expression = query(dashboard, panel, "D")
        status = {**WORKLOAD, "job": "dsx", "observation_valid": "true"}
        valid = [
            series("dsx_workload_status", value, **status, phase=phase, instance=replica)
            for phase, value in (("Running", "1+0x2"), ("Completed", "0+0x2"))
            for replica in ("replica-a", "replica-b")
        ]
        tests.append({
            "name": f"Two phase names need no fixed phase count: {dashboard}",
            "input_series": valid,
            "promql_expr_test": [expectation(expression, "1m", 1, **WORKLOAD, job="dsx")],
        })
        tests.append({
            "name": f"Any phase disagreement rejects the workload: {dashboard}",
            "input_series": valid + [
                series("dsx_workload_status", value, **status, phase="Pending", instance=replica)
                for replica, value in (("replica-a", "0+0x2"), ("replica-b", "1+0x2"))
            ],
            "promql_expr_test": [expectation(expression, "1m")],
        })
    return {"evaluation_interval": "30s", "tests": [{"interval": "30s", **test} for test in tests]}


def main():
    fixture = fixtures()
    with tempfile.TemporaryDirectory(prefix="dashboard-queries-") as temporary:
        directory = Path(temporary)
        path = directory / "queries.json"
        path.write_text(json.dumps(fixture))
        # The Prometheus image runs as nobody and only reads synthetic fixtures.
        directory.chmod(0o755)
        path.chmod(0o644)
        if promtool := os.environ.get("PROMTOOL"):
            command = [promtool, "test", "rules", str(path)]
        else:
            command = [
                "docker", "run", "--rm", "--entrypoint=/bin/promtool",
                "-v", f"{directory}:/tests:ro", PROMETHEUS_IMAGE,
                "test", "rules", "/tests/queries.json",
            ]
        result = subprocess.run(command, text=True, capture_output=True, check=False)
        output = result.stdout + result.stderr
        # Queries contain repeated subexpressions; report the panel reference instead.
        for expression, name in QUERY_NAMES.items():
            output = output.replace(json.dumps(expression), json.dumps(name))
            output = output.replace(expression, name)
        print(output, end="")
        if result.returncode == 0:
            print(f"Passed {len(fixture['tests'])} dashboard query cases.")
        raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
