# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

"""Check the portable dashboard contract using only the Python standard library."""

import json
import re
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
DASHBOARDS = {
    "workload-overview.json": "dsx-example-workloads",
    "workload-structure.json": "dsx-example-structure",
    "metric-samples.json": "dsx-example-metrics",
}
METRICS = {
    "dsx_workload_pod_info",
    "dsx_workload_status",
    "dsx_workload_created_timestamp_seconds",
    "dsx_exporter_observation_valid",
}
RULES = {"dsx:workload:observed", "dsx:pod_attribution:unique"}
OBSERVER_METRICS = {
    "workload_gpu_observer_state",
    "workload_gpu_observer_assigned_devices",
    "workload_gpu_observer_utilization_max_ratio",
    "workload_gpu_observer_low_activity_observation_seconds",
    "workload_gpu_observer_observation_healthy",
}
DATASOURCE = {"type": "prometheus", "uid": "${datasource}"}


def objects(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from objects(child)
    elif isinstance(value, list):
        for child in value:
            yield from objects(child)


def check(filename, uid):
    dashboard = json.loads((ROOT / filename).read_text())
    assert "dashboard" not in dashboard and "meta" not in dashboard, filename
    assert dashboard.get("id") is None and dashboard["uid"] == uid, filename
    assert dashboard.get("version", 0) <= 1, filename
    assert dashboard["refresh"] == "30s", filename
    variables = {v["name"]: v for v in dashboard["templating"]["list"]}
    assert variables["datasource"]["type"] == "datasource", filename
    assert variables["datasource"].get("hide", 0) == 0, filename
    assert variables["exporter_job"].get("multi", False) is False, filename
    for variable in variables.values():
        if variable["type"] in {"datasource", "query"}:
            current = variable.get("current", {})
            assert not current or current.get("value") == ["$__all"], (filename, variable["name"], "saved selection")
            assert not variable.get("options"), (filename, variable["name"], "cached options")
        if variable["type"] == "query":
            assert variable["datasource"] == DATASOURCE, (filename, variable["name"])
    if filename != "metric-samples.json":
        assert variables["namespace"]["multi"] is True, filename
    if filename == "workload-overview.json":
        activity_view = variables["activity_view"]
        assert activity_view["type"] == "custom", filename
        assert not activity_view.get("multi") and not activity_view.get("includeAll"), filename
        assert activity_view["current"] == {"text": "GPU workloads", "value": "4"}, filename
        assert {(option["text"], option["value"]) for option in activity_view["options"]} == {
            ("GPU workloads", "4"), ("Active only", "1"), ("All workloads", "5")
        }, filename
        assert all("${activity_view:" not in json.dumps(panel)
                   for panel in dashboard["panels"] if panel["id"] != 308), filename
    if filename == "workload-structure.json":
        assert not variables["workload_uid"].get("multi"), filename
        assert not variables["workload_uid"].get("includeAll"), filename
        assert any(o.get("type") == "nodeGraph" for o in objects(dashboard)), filename
    if filename == "metric-samples.json":
        assert variables["metric"]["type"] == "custom", filename
        assert set(variables["metric"]["query"].split(",")) == METRICS, filename
        assert "workload_uid" not in variables, filename

    panel_ids = []
    targets = []
    for obj in objects(dashboard):
        if "gridPos" in obj:
            panel_ids.append(obj["id"])
        if "datasource" in obj:
            assert obj["datasource"] == DATASOURCE, (filename, obj.get("title", obj.get("refId")))
        if "expr" in obj:
            targets.append(obj)
            assert obj.get("datasource") == DATASOURCE, (filename, obj.get("refId"))
            assert not obj.get("hide", False), (filename, obj.get("refId"), "hidden query")
            observer_metrics = set(re.findall(r"\bworkload_gpu_observer_\w+", obj["expr"]))
            if observer_metrics:
                assert filename == "workload-overview.json", filename
                assert observer_metrics <= OBSERVER_METRICS, (filename, observer_metrics)
                assert '${observer_job:regex}' in obj["expr"], (filename, "unscoped observer")
        if "spanNulls" in obj:
            assert obj["spanNulls"] is False, filename
        if "url" in obj:
            assert not re.match(r"(?:https?://|/)", obj["url"]), (filename, obj["url"])
    assert panel_ids and len(panel_ids) == len(set(panel_ids)), (filename, "panel IDs")
    assert targets, filename
    text = json.dumps(dashboard)
    assert set(re.findall(r"\bdsx_[a-zA-Z0-9_]+", text)) <= METRICS | {"dsx_workload_"}, filename
    assert set(re.findall(r"\bdsx:[a-zA-Z0-9_:]+", text)) <= RULES, filename
    assert not re.search(r"karta_demo:|\bgpu_observer_|simulator_|/karta/grafana", text), filename
    for variable in re.findall(r"\$\{([a-zA-Z_]\w*)(?::[^}]+)?\}|\$([a-zA-Z_]\w*)", text):
        name = variable[0] or variable[1]
        assert name in variables or name.startswith("__"), (filename, name)
    if filename == "metric-samples.json":
        assert not (set(re.findall(r"\bdsx:[a-zA-Z0-9_:]+", text)) & RULES), filename
        assert len(targets) == 1 and targets[0]["instant"] is True, filename
        assert targets[0]["expr"].startswith('{__name__="${metric}",'), filename
        assert targets[0]["expr"].endswith("}"), filename
        assert 'namespace=~"${namespace:regex}|^$"' in targets[0]["expr"], filename
        assert not re.search(r"\b(?:sum|count|max|min|rate|group)\s*\(", targets[0]["expr"]), filename
    print(f"PASS {filename}: {len(panel_ids)} panels, {len(targets)} queries")


def check_fixture_expressions():
    entries = []
    for path in sorted((ROOT / "tests").glob("*.test.yaml")):
        lines = path.read_text().splitlines()
        for index, line in enumerate(lines):
            match = re.search(r"# dashboard-expression: (\S+) (\d+) (\S+)$", line)
            if not match:
                continue
            assert re.match(r"\s*- expr: (?:&\w+ )?\|-?$", lines[index + 1]), path
            indent = len(lines[index + 2]) - len(lines[index + 2].lstrip())
            block = []
            for value in lines[index + 2:]:
                if not value.strip() or len(value) - len(value.lstrip()) < indent:
                    break
                block.append(value.strip())
            entries.append((*match.groups(), " ".join(block)))
    assert len(entries) == 13, "Expected core, node-resource, shared-device and observer fixtures"
    for filename, panel_id, ref_id, expression in entries:
        dashboard = json.loads((ROOT / filename).read_text())
        panel = next(obj for obj in objects(dashboard) if obj.get("id") == int(panel_id))
        actual = next(target["expr"] for target in panel["targets"] if target["refId"] == ref_id)
        for variable, value in {"exporter_job": "exporter", "namespace": "ns", "workload_uid": "multi", "dcgm_job": "dcgm", "cadvisor_job": "cadvisor", "workload": ".*", "component": ".*", "component_instance": ".*", "gpu_device": ".*", "observer_job": "observer", "activity_view": "4", "__range_s": "300", "__interval": "30s", "__rate_interval": "5m"}.items():
            actual = actual.replace("${" + variable + ":regex}", value).replace("${" + variable + ":raw}", value).replace("${" + variable + "}", value).replace("$" + variable, value)
        assert " ".join(actual.split()) == " ".join(expression.split()), (filename, panel_id, ref_id, "fixture query drift")
    print(f"PASS {len(entries)} fixture expressions match the shipped dashboard queries")


if __name__ == "__main__":
    for name, expected_uid in DASHBOARDS.items():
        check(name, expected_uid)
    check_fixture_expressions()
