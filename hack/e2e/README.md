<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Karta e2e cluster provisioner

This directory provisions a local kind cluster for the Karta e2e suite: it builds
and deploys the Karta operator, installs the dependencies the suite needs, and
installs the upstream workload operators it exercises. Each operator is smoke-tested
as it installs, so a broken install fails provisioning rather than a later run.

## Layout

```
hack/e2e/
  up.sh                 orchestrator: base + selected operators (install then verify)
  down.sh               tear the cluster down (and its kubeconfig for named clusters)
  global.env            single source of truth for versions and runtime defaults
  kind-config.yaml      kind cluster shape (1 control-plane + 2 workers)
  karta-operator/       the system under test
    install.sh          standalone: installs Karta in the selected webhook route
    verify.sh           standalone: smoke-tests it via run_smoke
    smoke.yaml          the throwaway Karta the smoke test applies
    test.sh             runs the operator e2e against the current cluster
  operators/            the upstream workload operators it exercises
    _common.sh          shared helpers + GitHub Actions logging, sourced by every script
    <name>/
      install.sh        standalone: installs that workload operator (a subprocess)
      verify.sh         standalone: smoke-tests it via run_smoke
      smoke.yaml        the throwaway workload the smoke test applies
      <config>.yaml     optional co-located config (e.g. grove/values.yaml)
```

## Usage

```sh
make e2e-up                          # base + all operators
make e2e-up WORKLOADS="jobset lws"   # base + a subset (one provision, deps resolved once)
make e2e-up WORKLOADS="jobset"       # base + a single operator
make e2e-up WORKLOADS=none           # base only, no workload operators
make e2e-down                        # tear down
./hack/e2e/up.sh --list dynamo       # print the resolved plan and exit (dynamo pulls grove)
./hack/e2e/up.sh --list none         # print the base-only plan and exit
```

The base is the kind cluster, the fake-gpu-operator, and the Karta operator.
Selecting a subset keeps a run light, and `none` keeps only the base, which is what
the operator e2e wants. Dependencies are added automatically: kserve pulls
knative, dynamo pulls grove.

## How up.sh runs an operator

For each selected operator, up.sh runs `operators/<name>/install.sh` then
`operators/<name>/verify.sh` as `bash <script>` subprocesses. The only contract is
the exit code: any non-zero exit is a failed install/smoke and fails provisioning
fast (the `main()` wrapper in the scripts is just convention). up.sh groups each
operator in the CI log (`::group::`) and writes an install summary table to the run's
Summary page (operator, version, install time, smoke time).

## Shared helpers (`operators/_common.sh`)

Sourcing `_common.sh` also loads `global.env`, so any script gets the version
pins in one step. Available helpers:

- `rollout_wait <ns> <resource> [timeout]` - wait for a rollout; resource includes
  the kind, e.g. `deploy/foo` or `statefulset/foo`.
- `apply_with_retry <file|url> [tries] [sleep] [kubectl args...]` - kubectl apply,
  retried past a warming webhook.
- `retry <tries> <sleep> <command...>` - run a command, retried past a transient.
- `run_smoke <manifest> <target> <wait-expr> [timeout] [ns]` - apply a throwaway
  resource, wait for the state, delete it. Used by every verify.sh.
- `preload_image <src-ref> <local-tag>` - pull an image and load it into kind.
- `build_and_load_image <context-dir> <local-tag>` - build an image and load it.
- `ensure_secret <ns> <name> <k=v>...` - idempotently create/update a secret.
- Logging: `group`/`endgroup`, `notice`/`warn`/`fail`, `summary`. When to use each
  is documented at the top of `_common.sh`.

## Adding an operator

Install side (this directory):

1. Create `operators/<name>/install.sh` as a standalone script:

   ```sh
   #!/usr/bin/env bash
   # SPDX-License-Identifier: Apache-2.0
   # Copyright (c) 2026 NVIDIA Corporation
   set -euo pipefail
   MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
   # shellcheck source=/dev/null
   source "${MODULE_DIR}/../_common.sh"

   main() {
     echo "==> <name> ${<NAME>_VERSION}"
     # install with helm/kubectl; wait with rollout_wait; use the shared helpers;
     # reference co-located config via "${MODULE_DIR}/...".
   }

   main "$@"
   ```

2. Create `operators/<name>/smoke.yaml` (a throwaway workload) and
   `operators/<name>/verify.sh`:

   ```sh
   #!/usr/bin/env bash
   # SPDX + copyright, set -euo pipefail, MODULE_DIR, source ../_common.sh
   run_smoke "${MODULE_DIR}/smoke.yaml" "<kind>/<name>-smoke" "<wait-expr>" "<timeout>" default
   ```

   `<wait-expr>` is passed to `kubectl wait --for=`, so both `condition=Ready` and
   `jsonpath={.status.state}=ready` work.

3. Pin the version(s) in `global.env`, and add a `version_of` case in `up.sh` so
   the operator shows in the install summary.
4. Add `<name>` to `ALL_WORKLOADS` in `up.sh` (in install order), and a `deps_of`
   entry only if it depends on another operator.

Before pushing: `make lint-shell` (shellcheck), then provision just that operator to
confirm it installs and smoke-tests clean, for example `make e2e-up WORKLOADS=<name>`.
