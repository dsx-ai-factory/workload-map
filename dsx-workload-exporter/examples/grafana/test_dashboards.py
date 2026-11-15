# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

"""Offline import and portability checks for the public Grafana dashboards."""

import json
import re
import unittest
from pathlib import Path


DIRECTORY = Path(__file__).resolve().parent
DASHBOARDS = {
    "workload-overview.json": "dsx-example-workloads",
    "workload-structure.json": "dsx-example-structure",
}
# Public registry: grafana/grafana v12.2.1,
# public/app/features/transformers/standardTransformers.ts. Internal helpers such as
# "order" must be configured through their public transformation ("organize").
NATIVE_TRANSFORMATIONS = frozenset("""
reduce filterFieldsByName renameByRegex filterByRefId filterByValue organize
joinByField seriesToRows concatenate calculateField labelsToFields groupBy sortBy
merge histogram rowsToFields configFromData prepareTimeSeries convertFieldType
spatial fieldLookup extractFields heatmap groupingToMatrix limit joinByLabels
regression partitionByValues formatString groupToNestedTable formatTime
timeSeriesTable transpose
""".split())
VARIABLE = re.compile(r"\$\{([A-Za-z_]\w*)(?::[^}]+)?\}|\$([A-Za-z_]\w*)")


def objects(value):
    if isinstance(value, dict):
        yield value
        for child in value.values():
            yield from objects(child)
    elif isinstance(value, list):
        for child in value:
            yield from objects(child)


def strings(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for child in value.values():
            yield from strings(child)
    elif isinstance(value, list):
        for child in value:
            yield from strings(child)


class DashboardPortabilityTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.dashboards = {
            name: json.loads((DIRECTORY / name).read_text()) for name in DASHBOARDS
        }

    def test_native_transformations(self):
        for name, dashboard in self.dashboards.items():
            for panel in objects(dashboard["panels"]):
                for transform in panel.get("transformations", []):
                    with self.subTest(dashboard=name, panel=panel.get("id"),
                                      transformation=transform["id"]):
                        self.assertIn(transform["id"], NATIVE_TRANSFORMATIONS,
                                      "Transformation is not registered in Grafana 12.2.1")

    def test_stable_import_ids(self):
        for name, dashboard in self.dashboards.items():
            with self.subTest(dashboard=name):
                self.assertEqual(dashboard["uid"], DASHBOARDS[name])
                self.assertIsNone(dashboard.get("id"))
                ids = [item["id"] for item in objects(dashboard["panels"])
                       if "gridPos" in item]
                self.assertEqual(len(ids), len(set(ids)), "Duplicate panel IDs")

    def test_datasource_and_jobs_are_discovered(self):
        for name, dashboard in self.dashboards.items():
            variables = {item["name"]: item for item in dashboard["templating"]["list"]}
            with self.subTest(dashboard=name):
                datasource = variables["datasource"]
                self.assertEqual(datasource["type"], "datasource")
                self.assertEqual(datasource["query"], "prometheus")
                for key in ("datasource", "exporter_job", "cadvisor_job", "dcgm_job"):
                    self.assertFalse(variables[key].get("current", {}).get("value"), key)
                    self.assertFalse(variables[key].get("options"), key)
                for key in ("exporter_job", "cadvisor_job", "dcgm_job"):
                    self.assertEqual(variables[key]["type"], "query")
                    self.assertFalse(variables[key].get("includeAll"), key)
                cluster = variables["cluster"]
                self.assertEqual(cluster["type"], "textbox")
                self.assertFalse(cluster.get("current", {}).get("value"))
                self.assertFalse(cluster.get("query"))

    def test_queries_use_selected_datasource_and_raw_metrics(self):
        for name, dashboard in self.dashboards.items():
            for item in objects(dashboard):
                if not item.get("expr"):
                    continue
                with self.subTest(dashboard=name, ref=item.get("refId")):
                    expression = item["expr"]
                    self.assertNotIn("dsx:", expression)
                    self.assertNotIn("workload_gpu_observer_", expression)
                    self.assertNotRegex(expression, r"==\s*(?:7|9)\b",
                                        "Phase availability must not assume an exporter version")
            for item in objects(dashboard):
                source = item.get("datasource")
                if isinstance(source, dict) and source.get("type") == "prometheus":
                    with self.subTest(dashboard=name, datasource=source):
                        self.assertIn(source.get("uid"), ("$datasource", "${datasource}"))

    def test_template_references_resolve(self):
        for name, dashboard in self.dashboards.items():
            declared = {item["name"] for item in dashboard["templating"]["list"]}
            for value in strings(dashboard):
                for match in VARIABLE.finditer(value):
                    variable = match.group(1) or match.group(2)
                    if variable.startswith("__"):
                        continue  # Grafana built-ins; numeric PromQL replacements are not matched.
                    with self.subTest(dashboard=name, variable=variable):
                        self.assertIn(variable, declared)

    def test_no_saved_environment_or_external_dashboard_links(self):
        known_uids = set(DASHBOARDS.values())
        for other in DIRECTORY.glob("*.json"):
            data = json.loads(other.read_text())
            if isinstance(data, dict) and "uid" in data:
                known_uids.add(data["uid"])
        for name, dashboard in self.dashboards.items():
            content = json.dumps(dashboard)
            with self.subTest(dashboard=name):
                self.assertNotRegex(content, r"https?://")
            for item in objects(dashboard):
                url = item.get("url", "")
                match = re.search(r"(?:^|/)d/([^/?]+)", url)
                if match:
                    with self.subTest(dashboard=name, link=url):
                        self.assertIn(match.group(1), known_uids)
                        self.assertIn("${datasource:queryparam}", url)
                        self.assertIn("${cluster:queryparam}", url)


if __name__ == "__main__":
    unittest.main()
