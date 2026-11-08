<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# Support

## Supported Versions

Karta is pre-1.0. We maintain the latest released minor version line, which is released from its release branch (for example `v0.3`). See [RELEASE.md](RELEASE.md) for how versions and release branches are cut.

| Version | Status |
|---|---|
| Latest released minor | Supported |
| Older minors | Not supported |

Bug reports and pull requests against unsupported versions are not accepted, except for critical security issues, which are handled privately through NVIDIA PSIRT (see [Security](SECURITY.md)).

When a new minor version ships, the previous one stops receiving fixes.

## Where to Get Help

| Channel | Use it for |
|---|---|
| [GitHub Discussions](https://github.com/dsx-ai-factory/workload-map/discussions) | Usage questions and design conversations |
| [GitHub Issues](https://github.com/dsx-ai-factory/workload-map/issues) | Bugs, feature requests, documentation issues |
| [NVIDIA PSIRT](https://www.nvidia.com/en-us/security/) | Private vulnerability reports, see [Security](SECURITY.md) |

Maintainers monitor Discussions and Issues. Expect an initial response within 5 business days, usually sooner.

Before opening an issue, check [Troubleshooting](docs/Troubleshooting.md) and search existing issues. Include the Karta version, the Kubernetes version, and the Karta definition involved.
