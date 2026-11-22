<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# Karta Headlamp Plugin

A [Headlamp](https://headlamp.dev) plugin scaffold for visualizing Karta
workload trees: the components, instances, and normalized status of any
workload described by a Karta definition (`kartas.run.ai`). Currently this
is a placeholder setup, a `/karta/workloads` route and a WASM engine
exporting only `kartaVersion`. The real tree visualization and engine
bindings land in follow-up work.

## Experimental definitions

Definitions with `spec.experimental: true` are excluded by default, from both
the catalog and the cluster. To include them, open Settings > Plugins > karta,
enable "Enable experimental definitions", and save. Headlamp stores this
setting in the current browser profile. Disabling it removes their workloads
from the table.

## How the WASM engine is built and loaded

[`karta-wasm/`](../karta-wasm/) (repository root, a sibling of this
directory) is a separate Go module compiled to WebAssembly
(`GOOS=js GOARCH=wasm`) and shipped as a plugin asset via the
`headlamp.extraDist` entry in `package.json`, which points at
`../karta-wasm/karta.wasm` and `../karta-wasm/wasm_exec.js`. It lives
outside `headlamp-plugin/` so it can be reused by other consumers later
without being tied to this specific plugin. At runtime, `src/lib/engine.ts`
fetches `wasm_exec.js` and `karta.wasm` from wherever Headlamp served this
plugin from and instantiates the module in the browser, see `src/lib/engine.ts`
for the details.

## Development

Requires Node.js >= 22, npm, and Go (for the WebAssembly build).

```bash
make karta-wasm   # from the repository root; builds karta-wasm/karta.wasm + karta-wasm/wasm_exec.js
npm install
npm start
```

`npm start` watches `src/` and rebuilds the plugin bundle on change, copying
in the `karta-wasm/` build artifacts (via `extraDist`) into Headlamp's
plugin directory. It does not watch Go source or rerun `make
karta-wasm` for you, after editing `../karta-wasm/main.go`, rerun
`make karta-wasm` from the repository root, then save any file
under `src/` (or restart `npm start`) to pick up the new binary.

Headlamp only reads its plugin directory at startup, so after the very first
install, or whenever the set of files in Headlamp's plugin directory
(`~/Library/Application Support/Headlamp/plugins/karta` on macOS,
`~/.config/Headlamp/plugins/karta` on Linux) changes (not just their
contents), fully quit and reopen the Headlamp desktop app to pick it up. It
does not hot-reload plugins.

Lint, type-check, and test with:

```bash
npm run lint
npm run tsc
npm run test
```

## CI

The `headlamp-plugin-ci` job in `.github/workflows/ci.yaml` runs on every
push/PR to `main`/`v0.*`, regardless of which files changed. It runs
`make headlamp-plugin-build` from the repository root, which is:

```bash
make karta-wasm   # go build the WASM module
npm ci
npm run lint
npm run tsc
npm run test
npm run build      # production vite build; extraDist copies karta-wasm/karta.wasm + wasm_exec.js into dist/
```
