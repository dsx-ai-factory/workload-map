#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
"""Bind an operator recording run to its changed pins and generated files."""

import argparse
import hashlib
import json
from pathlib import Path
import re
import shutil
import subprocess

ENV_PATH = "hack/e2e/global.env"
RECORDINGS = "test/e2e/recorded_data"
PIN = re.compile(r'^([A-Z_]+)="\$\{\1:-([^{}\n]+)\}"$', re.MULTILINE)
SAFE_VERSION = re.compile(r"[A-Za-z0-9][A-Za-z0-9.+_-]*")


def git(*args, cwd=None):
    return subprocess.check_output(["git", *args], cwd=cwd)


def prepare(plan, base, root):
    root = root.resolve()
    if git("rev-parse", "HEAD").decode().strip() != base:
        raise ValueError("Renovate must record directly on the selected base commit")
    before = git("show", f"{base}:{ENV_PATH}").decode()
    after = Path(ENV_PATH).read_text()
    old, new = dict(PIN.findall(before)), dict(PIN.findall(after))
    allowed = {pin["variable"] for pin in plan["pins"]}
    changed = {key for key in old.keys() | new.keys() if old.get(key) != new.get(key)}
    if not changed or not changed <= allowed:
        raise ValueError("expected changes only to this operator group's pins")
    expected = before
    for key in changed:
        if key not in old or key not in new or not SAFE_VERSION.fullmatch(new[key]):
            raise ValueError(f"invalid version for {key}")
        expected = expected.replace(f'{key}="${{{key}:-{old[key]}}}"', f'{key}="${{{key}:-{new[key]}}}"')
    if expected != after:
        raise ValueError("global.env contains changes other than the selected version pins")
    if git("diff", "--name-only", base).decode().splitlines() != [ENV_PATH]:
        raise ValueError("the candidate must initially change only global.env")
    if git("ls-files", "--others", "--exclude-standard").strip():
        raise ValueError("the candidate checkout has unexpected untracked files")
    versions = {}
    for recording in plan["recordings"]:
        values = [new[var] for var in recording["versionVariables"]]
        versions[recording["operator"]] = recording.get("versionSeparator", "").join(values)
    for version in versions.values():
        if not SAFE_VERSION.fullmatch(version):
            raise ValueError("unsafe recording version")
    paths = [f"{RECORDINGS}/{operator}/{version}" for operator, version in versions.items()]
    for path in paths:
        target = root / path
        if any(parent.is_symlink() for parent in [target, *target.parents]):
            raise ValueError(f"recording path contains a symlink: {path}")
    return {"base": base, "group": plan["group"], "versions": versions, "directories": paths,
            "pins": {key: {"before": old[key], "after": new[key]} for key in sorted(changed)},
            "defaults": new}


def installed(candidate, plan, path):
    values = dict(line.split("=", 1) for line in path.read_text().splitlines() if "=" in line)
    expected = dict(candidate["versions"])
    if plan["group"] == "knative":
        expected["kourier"] = candidate["defaults"]["KOURIER_VERSION"]
    if plan["group"] in ("cert-manager", "fake-gpu-operator", "kai-scheduler"):
        expected[plan["group"]] = candidate["defaults"][plan["pins"][0]["variable"]]
    for operator, version in expected.items():
        if values.get(operator) != version:
            raise ValueError(f"installed version for {operator} is {values.get(operator)!r}, expected {version!r}")


def files(candidate):
    result = [ENV_PATH]
    for directory in candidate["directories"]:
        entries = sorted(Path(directory).glob("*/*.yaml"))
        if not entries:
            raise ValueError(f"no generated recordings in {directory}")
        for path in entries:
            if path.is_symlink() or not path.is_file():
                raise ValueError(f"recording is not a regular file: {path}")
        result.extend(str(path) for path in entries)
    return result


def manifest(candidate, root):
    paths = files(candidate)
    changes = set(git("diff", "--name-only", candidate["base"]).decode().splitlines())
    changes.update(git("ls-files", "--others", "--exclude-standard").decode().splitlines())
    if not changes <= set(paths):
        raise ValueError(f"unexpected changed files: {sorted(changes - set(paths))}")
    return {"base": candidate["base"], "group": candidate["group"],
            "files": {path: hashlib.sha256((root / path).read_bytes()).hexdigest() for path in paths}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["prepare", "installed", "headers", "manifest", "copy"])
    parser.add_argument("--artifacts", required=True, type=Path)
    parser.add_argument("--base")
    parser.add_argument("--installed", type=Path)
    parser.add_argument("--destination", type=Path)
    args = parser.parse_args()
    artifacts = args.artifacts.resolve()
    root = Path.cwd().resolve()
    if not args.artifacts.is_absolute() or artifacts == root or root in artifacts.parents:
        parser.error("artifacts must be outside the Renovate checkout")
    plan = json.loads((artifacts / "plan.json").read_text())
    if args.command == "prepare":
        candidate = prepare(plan, args.base, root)
        (artifacts / "candidate.json").write_text(json.dumps(candidate, indent=2) + "\n")
        # Remove old fixtures so the validator cannot mistake a stale file for a new recording.
        for directory in candidate["directories"]:
            if Path(directory).exists():
                shutil.rmtree(directory)
        return
    candidate = json.loads((artifacts / "candidate.json").read_text())
    if args.command == "installed":
        installed(candidate, plan, args.installed)
    elif args.command == "headers":
        header = "# SPDX-License-Identifier: Apache-2.0\n# Copyright (c) 2026 NVIDIA Corporation\n\n"
        for name in files(candidate)[1:]:
            path = Path(name)
            content = path.read_text()
            if not content.startswith("# SPDX-License-Identifier:"):
                path.write_text(header + content)
    elif args.command == "manifest":
        content = manifest(candidate, root)
        (artifacts / "manifest.json").write_text(json.dumps(content, indent=2) + "\n")
    elif args.command == "copy":
        for directory in candidate["directories"]:
            target = args.destination / directory
            if target.exists():
                shutil.rmtree(target)
        for name in files(candidate):
            target = args.destination / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(name, target)


if __name__ == "__main__":
    main()
