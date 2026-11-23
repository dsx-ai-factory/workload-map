#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation

import argparse
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile


def extract_rules(article):
    name = r"^  name:[ \t]+(?:prometheus-rules|'prometheus-rules'|\"prometheus-rules\")[ \t]*$"
    candidates = []
    for language, block in re.findall(r"^```([^\n]*)\n(.*?)^```[ \t]*$", article, re.MULTILINE | re.DOTALL):
        candidates.extend((language, block) for _ in re.findall(name, block, re.MULTILINE))
    if len(candidates) != 1:
        raise ValueError(f"expected one inline prometheus-rules ConfigMap, found {len(candidates)}")
    language, block = candidates[0]
    if language not in ("sh", "bash"):
        raise ValueError("rules ConfigMap must be in a sh or bash fence")
    payload = re.fullmatch(r"kubectl apply -f - <<'EOF'\n(.*?)^EOF\n?", block, re.MULTILINE | re.DOTALL)
    if payload is None:
        raise ValueError("rules ConfigMap must use kubectl apply -f - with a quoted EOF heredoc")
    documents = re.split(r"^---[ \t]*$", payload.group(1), flags=re.MULTILINE)
    document = next(document for document in documents if re.search(name, document, re.MULTILINE))
    if not re.search(r"^kind: ConfigMap$", document, re.MULTILINE):
        raise ValueError("prometheus-rules must be a ConfigMap")
    if len(re.findall(r"^  tutorial\.yaml:", document, re.MULTILINE)) != 1:
        raise ValueError("expected one tutorial.yaml entry in the rules ConfigMap")
    header = re.search(r"^data:\n  tutorial\.yaml: \|\n", document, re.MULTILINE)
    if header is None:
        raise ValueError("expected data.tutorial.yaml as an indented literal block")
    lines = []
    for line in document[header.end():].splitlines():
        if line.startswith("    "):
            lines.append(line[4:])
        elif not line.strip():
            lines.append("")
        else:
            break
    if not any(line.strip() and not line.lstrip().startswith("#") for line in lines):
        raise ValueError("inline tutorial.yaml rules are empty")
    return "\n".join(lines) + "\n"


def main():
    parser = argparse.ArgumentParser(description="Check the tutorial's inline Prometheus rules.")
    parser.add_argument("--promtool", required=True)
    parser.add_argument("--article", required=True, type=Path)
    parser.add_argument("--tests", required=True, type=Path)
    args = parser.parse_args()
    binary = shutil.which(args.promtool)
    if binary is None:
        raise ValueError(f"promtool executable not found: {args.promtool}")
    binary = str(Path(binary).resolve())
    rules = extract_rules(args.article.read_text())
    with tempfile.TemporaryDirectory(prefix="tutorial-rules-") as directory:
        temporary = Path(directory)
        (temporary / "rules.yaml").write_text(rules)
        shutil.copyfile(args.tests, temporary / "rules_test.yaml")
        print(f"Checking inline rules in {args.article}", flush=True)
        subprocess.run([binary, "check", "rules", "--lint-fatal", "rules.yaml"], cwd=temporary, check=True)
        print(f"Testing {args.tests}", flush=True)
        subprocess.run([binary, "test", "rules", "rules_test.yaml"], cwd=temporary, check=True)


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        sys.exit(error.returncode)
    except (OSError, ValueError) as error:
        print(f"Tutorial rule check failed: {error}", file=sys.stderr)
        sys.exit(1)
