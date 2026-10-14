<!--
SPDX-License-Identifier: Apache-2.0
Copyright (c) 2026 NVIDIA Corporation
-->

# Adopters

Projects and organizations using Karta.

To add your project or organization, open a pull request that adds a row to the table below. Include a link to a public record showing Karta usage or explicit permission from the adopter to name it. If you use Karta but cannot share details publicly, contact the maintainers.

| Adopter | Type | How Karta is used |
|---------|------|-------------------|
| [KAI Scheduler](https://github.com/kai-scheduler/KAI-Scheduler) | Open-source Kubernetes GPU scheduler | Pod-grouper fallback plugin: gang scheduling for any workload type through Karta workload descriptions. Native plugins take precedence; Karta is the generic fallback. Merged 2026-07-13 in [KAI PR #1877](https://github.com/kai-scheduler/KAI-Scheduler/pull/1877). |
| [Nova Kubernetes Federated Orchestrator](https://docs.elotl.co/nova/intro/) | Policy-driven resource-aware multi-cluster Kubernetes scheduler by [Elotl](https://www.elotl.co/) | Choosing a target Kubernetes cluster with sufficient resources to gang-schedule custom resources: DynamoGraphDeployment, LeaderWorkerSet, InferenceService, PyTorchJob, JobSet, Milvus. Coverage detail in the [Nova API resources doc](https://docs.elotl.co/nova/Appendix/api-resources/). Listed with Elotl's permission (2026-07-20). |
| [NVIDIA Cloud Functions (NVCF)](https://github.com/NVIDIA/nvcf) | Open-source serverless inference platform for deploying and routing GPU-accelerated workloads | NVCF uses Karta in its cluster agent to understand the health of Kubernetes workloads created by operators. Karta reads each workload's status and helps NVCF determine whether it is starting, running, degraded, or failed. |
| [NVIDIA Run:ai](https://www.nvidia.com/en-us/software/run-ai/) | Kubernetes AI workload and GPU orchestration platform | Lets administrators register new workload types, such as custom resources, through Karta workload descriptions. See [Extending Workload Support with Karta](https://run-ai-docs.nvidia.com/self-hosted/workloads-in-nvidia-run-ai/workload-types/extending-workload-support) in the Run:ai documentation. |
