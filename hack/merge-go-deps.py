# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
#
# Merge `go mod download -json` output from several modules into one list for
# go-licence-detector, keeping each (module path, version) once.
# Usage: python3 hack/merge-go-deps.py root-deps.json cli-deps.json ... > deps.json
#
# Modules may require different versions of the same dependency; every version
# is kept, since each one ships in some binary. What must hold is the licence,
# and go-licence-detector asserts it for every entry against its rules
# (allowlist), so a version that changed licence still fails the build.

import json
import sys

entries = []  # in first-seen order; a further version goes right after its siblings
seen = set()
for fname in sys.argv[1:]:
    with open(fname) as f:
        data = f.read()
    dec = json.JSONDecoder()
    pos = 0
    while pos < len(data):
        while pos < len(data) and data[pos] in " \t\r\n":
            pos += 1
        if pos >= len(data):
            break
        obj, pos = dec.raw_decode(data, pos)
        key = (obj.get("Path", ""), obj.get("Version", ""))
        if not key[0] or key in seen:
            continue
        seen.add(key)
        # go-licence-detector keeps this order, so a second version of a package
        # lands next to the first and the NOTICE template can fold them.
        last = max((i for i, e in enumerate(entries) if e["Path"] == key[0]), default=len(entries) - 1)
        entries.insert(last + 1, obj)

for obj in entries:
    print(json.dumps(obj))
