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
  check-updates.sh       report upstream workload and infrastructure operator tags
  _updates.sh           update-check helpers, independent of cluster provisioning
  infra/<name>/         cert-manager, fake-gpu-operator, and KAI Scheduler
    install.sh          standalone: installs the infrastructure operator
    verify.sh           standalone: smoke-tests it via run_smoke
    check-updates.sh     standalone: report upstream tag updates
    smoke.yaml          the throwaway infrastructure smoke resources
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
      check-updates.sh   standalone: report upstream tag updates
      smoke.yaml        the throwaway workload the smoke test applies
      <config>.yaml     optional co-located config (e.g. grove/values.yaml)
```

The shared tag checker lives in its own Go module at `hack/operator-updates/`.
Its [README](../operator-updates/README.md) describes the CLI and release mapping
contract.

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
knative, dynamo pulls grove. `up.sh` installs and verifies infrastructure first:
Fake GPU Operator is always included, cert-manager follows `CERT_MANAGER` and
webhook/dependency settings, and KAI Scheduler is included whenever Grove is
selected. `--list` shows both infrastructure and workloads.

## Check for operator updates

```sh
make e2e-operators-check-latest-supported-versions
make e2e-operators-check-latest-supported-versions OPERATORS="jobset infra"
```

The check compares pins in `global.env` with upstream tags and reports which
operators have newer releases. Tags are candidates, not proof of upstream
support or Karta compatibility. It does not change pins or use a cluster.
See the [checker README](../operator-updates/README.md) for selection rules,
options, exit codes, and the required checker when adding an operator.

## How up.sh runs an operator

For each selected infrastructure operator, `up.sh` runs `infra/<name>/install.sh`
then `infra/<name>/verify.sh`. It uses the same runner for workload operators
under `operators/<name>/`. Both phases run as `bash <script>` subprocesses. The only contract is
the exit code: any non-zero exit is a failed install/smoke and fails provisioning
fast (the `main()` wrapper in the scripts is just convention). up.sh groups each
operator in the CI log (`::group::`) and writes an install summary table to the run's
Summary page (operator, version, install time, smoke time). Successful infrastructure
and workload versions are recorded in the per-cluster installed-version file.

The infrastructure smoke checks prove certificate issuance, a pod starting with
a simulated GPU, and a pod scheduled by KAI. They use `run_smoke` to clean up
throwaway resources after success or failure. `go test ./hack/e2e` exercises
planning, install/verify ordering, and failure propagation offline.

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
  With Docker Desktop's containerd image store, `kind load docker-image` can fail
  on a multi-arch image with `ctr: content digest ... not found`. Skip the preload
  then and let the nodes pull the pinned image.
- `build_and_load_image <context-dir> <local-tag>` - build an image and load it.
- `ensure_secret <ns> <name> <k=v>...` - idempotently create/update a secret.
- Logging: `group`/`endgroup`, `notice`/`warn`/`fail`, `summary`. When to use each
  is documented at the top of `_common.sh`.

## Adding an operator

Every external workload and infrastructure operator requires installation,
smoke verification, and an executable `check-updates.sh`. The update checker
reports candidate versions; `verify.sh` still proves the installed operator can
run its smoke workload. Kubernetes builtins and Karta built from this checkout
have no upstream operator release to check and are exempt.

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

   Apply upstream manifests with `kubectl apply --server-side` (plus
   `--force-conflicts` for a reused cluster). Large CRDs exceed the annotation
   size limit of a client-side apply.

2. Create `operators/<name>/smoke.yaml` (a throwaway workload) and
   `operators/<name>/verify.sh`:

   ```sh
   #!/usr/bin/env bash
   # SPDX + copyright, set -euo pipefail, MODULE_DIR, source ../_common.sh
   run_smoke "${MODULE_DIR}/smoke.yaml" "<plural>.<group>/<name>-smoke" "<wait-expr>" "<timeout>" default
   ```

   `<wait-expr>` is passed to `kubectl wait --for=`, so both `condition=Ready` and
   `jsonpath={.status.state}=ready` work.

3. Pin the version(s) in `global.env`, and add a `version_of` case in `up.sh` so
   the operator shows in the install summary.
4. Add an executable `operators/<name>/check-updates.sh`, register it in
   `check-updates.sh`, and add offline tests. Follow the
   [checker contract](../operator-updates/README.md#add-an-operator), including
   inspecting a local upstream clone to identify the installed artifact's
   release convention.
5. Add `<name>` to `ALL_WORKLOADS` in `up.sh` (in install order), and a `deps_of`
   entry only if it depends on another operator.

Infrastructure follows the same module pattern under `infra/<name>/`: executable
`install.sh`, `verify.sh`, and `check-updates.sh`, plus `smoke.yaml`. Installation
and verification source `../../operators/_common.sh`. Add its version to
`version_of` and select it in `up.sh`'s infrastructure plan before its dependents;
workload installers must not install infrastructure themselves. Register its
update checker in both `ALL_OPERATORS` and the `infra` selector. The same
release-source documentation and offline test requirements apply. A new kind
served by an existing operator reuses its checker; extend it when installation
adds a separately versioned component.

The offline Go tests require checkers for workload installer modules and
infrastructure directories and check their aggregate registration. Include the
executable bit and registration in the same change as a new operator.

Before pushing, run `make test-e2e-updates` and `make lint-shell`, then provision
the smallest plan that selects the operator to confirm installation and smoke
verification:

- Workload operators: `make e2e-up WORKLOADS=<name>`.
- Fake GPU Operator: `make e2e-up WORKLOADS=none`.
- cert-manager: `make e2e-up WORKLOADS=none CERT_MANAGER=true`.
- KAI Scheduler: `make e2e-up WORKLOADS=grove`.

For new infrastructure, document its selection condition and validation command.
Run `make check` as required by the contributing guide.
