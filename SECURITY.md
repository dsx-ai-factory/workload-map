<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# Security Policy

NVIDIA takes the security of our software products and services seriously, including the source code in this repository.

If you believe you have found a security vulnerability, please do not open a public GitHub issue. Use one of the private channels below.

## Supported Versions

Security fixes are issued for the supported versions listed in [Support](SUPPORT.md). End-of-life versions do not receive security updates.

## Reporting a Vulnerability

Please do not report security vulnerabilities through GitHub, neither public issues nor GitHub Security Advisories. Report them to NVIDIA PSIRT (Product Security Incident Response Team), which owns coordinated disclosure and CVE assignment across NVIDIA products:

- Web: [NVIDIA Security Vulnerability Submission Form](https://www.nvidia.com/object/submit-security-vulnerability.html)
- Email: <psirt@nvidia.com>
  - PGP key: [NVIDIA public PGP key for secure email communication](https://www.nvidia.com/en-us/security/pgp-key)
- Vulnerability Disclosure Program: <https://www.nvidia.com/en-us/security/>

Please include in your report:

- Product name and the version, branch, or commit SHA that contains the vulnerability
- Vulnerability class (for example: remote code execution, denial of service, privilege escalation, information disclosure)
- Reproduction steps
- Proof-of-concept code or exploit, if available
- Your assessment of the potential impact

## What to Expect

- Acknowledgement: within 3 business days of your report
- Initial assessment: within 10 business days
- Status updates: at least every 14 days while the issue is open

We will coordinate a public disclosure timeline with you once a fix is available. We kindly ask reporters to allow a reasonable timeframe for a fix before any public disclosure.

### Out of scope

The following are generally not considered reportable vulnerabilities:

- Issues caused by misconfiguration of a self-hosted deployment.
- Vulnerabilities in third-party dependencies that do not affect Karta directly (please report those to the upstream project).
- Reports from automated scanners without a demonstrated, exploitable impact.

## Acknowledgement

NVIDIA does not currently run a paid bug bounty program for this repository. We do publicly acknowledge externally reported security issues that are addressed under our [coordinated disclosure policy](https://www.nvidia.com/en-us/security/psirt-policies/), where the reporter wishes to be named. Please tell us your preference when you report.

## Security Architecture and Context

Karta is a Kubernetes CRD and tooling that lets controllers and platforms inspect, modify, and manage workloads of any type without per-CRD adapters. This repository contains the Karta Go library, the operator that reconciles Karta resources and serves admission webhooks, the `kli` command line tool, a WebAssembly build of the library, the Helm chart, and a catalog of pre-built Karta definitions.

This software operates primarily at the Kubernetes controller level, with additional CLI and library components. Its primary security responsibilities are:

- Safe evaluation of user-authored definitions: Karta resources embed jq queries and path expressions that are evaluated against arbitrary workload objects.
- Correct admission control: the operator's validating and mutating webhooks accept or reject Karta resources and must not be bypassable or a source of denial of service for the API server.
- Least-privilege access to the cluster: the operator reads workload custom resources and writes only Karta resources, status, events, and its own webhook configuration.
- Integrity of the published artifacts: the module, container images, and Helm chart that users install.

Repository exposure classification: Public. The project is Apache-2.0 licensed with public contribution documentation, so this document is written to public-safe detail.

### Trust Boundaries

1. Kubernetes API server to operator. The operator authenticates with its service account and acts within the RBAC granted by the Helm chart. Karta definitions and the workload objects they describe are read through the API server.

2. API server to admission webhooks. The API server calls the operator's validating and mutating webhooks over TLS. Certificates are managed through one of the documented certificate modes (see `docs/Webhook Certificates.md`). The webhook is a trust boundary for every Karta create and update.

3. Author to Karta definition. Anyone allowed to create or update a Karta resource controls the jq paths and queries the operator and any consuming controller will evaluate. Definitions are configuration, but the data they are evaluated against comes from other tenants' workloads.

4. Consumer to library. Controllers and platforms that import the Go library, the CLI, or the WebAssembly build pass untrusted workload objects and definitions into Karta's evaluation and tree-building code.

5. Build and release to user. Users pull container images, the Go module, and the Helm chart produced by this repository's CI and release process.

### Threat Model

The following are the primary security concerns for this project, ordered roughly by combined severity and likelihood.

1. Resource exhaustion through expressions. jq queries and path expressions in a Karta definition run inside the operator, the library, the CLI, and the WebAssembly build. Expressions that loop, recurse, or build very large values can consume CPU and memory in a long-running controller or in a consumer that evaluates definitions on a hot path. Limits on evaluation cost and output size are the control.

2. Webhook bypass or weakening. A validating webhook that accepts malformed or unsafe definitions, a webhook configuration that fails open, or a webhook certificate that is not verified lets invalid definitions reach consumers. Weakening webhook TLS or RBAC defaults for convenience widens this exposure.

3. Over-broad RBAC. The operator reads workload custom resources across the cluster to evaluate definitions. Any widening of the chart's cluster roles, or of the verbs granted on workload resources, increases the blast radius of an operator compromise.

4. Information disclosure through definitions. A definition can select any field of a workload object, including environment values, mounted secret references, and annotations, and surface it in status or through a consumer. Authors and consumers must treat the output of a definition as having the sensitivity of the object it was read from.

5. Supply-chain exposure. The project depends on third-party Go modules, npm packages for the Headlamp plugin, base images, and CI actions. Compromise of one of these reaches the built artifacts. Dependencies are updated through automated pull requests, pinned by version or digest, and checked for license drift.

6. Untrusted input in the CLI and WebAssembly build. `kli` and the browser-hosted WebAssembly build parse YAML and JSON supplied by the user. Malformed or hostile documents must not cause crashes, unbounded resource use, or code execution.

### Critical Security Assumptions

The following are conditions this software assumes are already satisfied by another layer. They are not enforced by the code in this repository, and they define the boundary of what a deployment must provide.

- Kubernetes enforces authentication, authorization, and tenant isolation. Karta relies on RBAC to decide who may create or modify Karta resources. Anyone with that permission is trusted to author definitions that the operator and consumers will evaluate.

- Cluster operators and install-time values are trusted. Helm values and the operator configuration are treated as authoritative. A deployment that relaxes webhook scope, certificate handling, or RBAC is assumed to have done so deliberately.

- Webhook certificates are issued and rotated by the chosen certificate mode. The operator does not provide its own certificate authority beyond the documented modes.

- Consumers bound the cost of evaluation they trigger. A controller or platform that evaluates user-supplied definitions at high frequency is responsible for rate limiting and timeouts on its own side.

- The build and dependency supply chain is verified by the surrounding CI, registry, and scanning infrastructure. This repository does not itself verify signatures for every artifact it consumes.
