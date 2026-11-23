---
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2026 NVIDIA Corporation
title: Prerequisites for Workload Map
sidebar-title: Prerequisites
description: Learn what you need in place before you install the Workload Map Go library, the kli CLI, or the Karta Operator.
---

This page lists the requirements for each Workload Map component: the Go library, the `kli` CLI, and the Karta Operator. The components are independent, so you can install any one of them without the others. Each component has its own section. To learn what each component does, refer to the [Components](../about/architecture-overview.md#components) table in the Architecture Overview.

## Go Library

- Go 1.27.3 or later. This is the `go` version in the `go.mod` file of the repository.
- No cluster is required.

## kli CLI

The requirements depend on how you install `kli`.

| Installation method | What you need |
| --- | --- |
| Homebrew | Homebrew on macOS or Linux. |
| Prebuilt archive | Nothing else. Archives are published for Linux and macOS on amd64 and arm64. |
| Build from a clone | Go 1.27.3 or later, `make`, and a clone of the repository. |

To run `kli` against a cluster, you also need a kubeconfig with read access to the workloads that you want to see.

## Karta Operator

- A Kubernetes cluster. Kubernetes 1.34 is the version used in end-to-end testing.
- Helm. Helm 3.16 is the version used in end-to-end testing.
- Permission to create a CustomResourceDefinition, a ClusterRole and its binding, and webhook configurations.
- Pull access to two images: the Karta Operator image, `ghcr.io/dsx-ai-factory/workload-map/karta-operator`, and the CRD upgrader image, `registry.k8s.io/kubectl`. For a private registry, set `imagePullSecrets` in the chart values.
- A network path from the Kubernetes API server to the Karta Operator webhook service.
- cert-manager, only if you supply the webhook certificate yourself with cert-manager. The default certificate mode does not need it. For more information, refer to [Webhook Certificates](../configuration/karta-operator/configure-webhook-certificates.md).
