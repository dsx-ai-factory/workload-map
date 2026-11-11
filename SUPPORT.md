<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# Support

## Supported Versions

Karta is pre-1.0. We maintain the three most recent released minor version lines. Each line is released from its release branch. See [RELEASE.md](RELEASE.md) for how versions and release branches are cut.

| Version | Branch | Status |
|---|---|---|
| 0.3.x | `v0.3` | Supported |
| 0.2.x | `v0.2` | Supported |
| 0.1.x | `v0.1` | Supported |
| Older | | Not supported |

Bug reports and pull requests against unsupported versions are not accepted, except for security issues, which are handled privately through NVIDIA PSIRT (see [Security](SECURITY.md)).

When a new minor version ships, this table is updated and the oldest line stops receiving fixes.

## Where to Get Help

| Channel | Use it for |
|---|---|
| [GitHub Discussions](https://github.com/dsx-ai-factory/workload-map/discussions) | Usage questions and design conversations |
| [GitHub Issues](https://github.com/dsx-ai-factory/workload-map/issues) | Bugs, feature requests, documentation issues |
| [NVIDIA PSIRT](https://www.nvidia.com/en-us/security/) | Private vulnerability reports, see [Security](SECURITY.md) |

Maintainers monitor Discussions and Issues. Expect an initial response within 5 business days, usually sooner.

Before opening an issue, check [Troubleshooting](docs/Troubleshooting.md) and search existing issues. Include the Karta version, the Kubernetes version, and the Karta definition involved.
