<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Pending and Progressing migration

The built-in catalog now reports independent observations instead of grouping
all startup and update activity into Initializing. Pending means waiting for
required readiness or a known controller observation. Progressing means an evidenced, unfinished operation and
can remain set while that operation is blocked. Neither label alone establishes
Pod scheduling. Missing evidence is not evidence of progress.

The SDK keeps legacy Initializing mappings and their output during a deprecation
window. Admission and CLI validation warn, but do not rewrite them. Install the
new CRD and update consumers before applying new definitions.

## Per-kind evidence

Each row describes predicates, not a guaranteed ordered lifecycle. Watch events
may skip intermediate observations. Pending and Progressing can overlap with
other statuses; the evaluator has no global precedence rule.

| Kind and served version | Pending evidence | Progressing evidence |
| --- | --- | --- |
| Pod v1 | Native phase Pending | Pending and PodScheduled=True |
| Deployment apps/v1 | First/current observation wait or fresh desired availability deficit | Fresh incomplete replica reconciliation or unfinished native rollout, including a deadline-exceeded rollout |
| StatefulSet apps/v1 | First/current observation wait or fresh ready-replica deficit | Replica reconciliation or an unfinished update consistent with its update strategy and partition |
| Job batch/v1 | First observation or ready-count deficit against remaining work | No parent-only signal used; active includes Pending Pods |
| CronJob batch/v1 | Unsuspended, observed status, first schedule not yet recorded | No parent rollout signal used |
| JobSet v1alpha2 | Missing expected groups or active-ready plus succeeded Jobs below their target | InOrder startup policy in progress, or RestartingJobSet=True |
| LeaderWorkerSet v1 | First/current observation wait or desired ready-group deficit | Progressing=True or UpdateInProgress=True |
| Grove PodCliqueSet v1alpha1 | First/current observation wait or observed available-replica deficit | Unfinished root update timestamps, UpdateInProgress=True, or Unknown/ProgressDeadlineExceeded; not attributed to the latest desired generation |
| PyTorchJob v1 | Created startup, or a nonelastic running master with an observed worker deficit | That startup state plus active replica components |
| MPIJob v2beta1 | Created=True without running, terminal, or suspended state | That startup state plus active replica components |
| RayCluster v1 | First observation, resume acknowledgment wait, or not provisioned with head not ready | Specific head container startup or backoff reasons during initial provisioning or that observed resume |
| RayJob v1 | Native New/first observation, application PENDING, Initializing, interactive Waiting, first application-status wait, or known suspension-acknowledgment wait | Deployment Initializing while the application is not running or terminal |
| RayService v1 | First observation or Ready=False/Initializing | Allocated pending or promoted active cluster during initialization, UpgradeInProgress=True, or RollbackInProgress=True |
| DynamoGraphDeployment v1alpha1 and v1beta1 | Native pending or bounded first/current observation wait for desired components | Explicit rollingUpdate phase Pending or InProgress; default initializing alone does not prove progress |
| Knative Service v1 | Ready=Unknown | Specific revision or traffic transition evidence during unresolved readiness |
| Milvus v1beta1 | Native Pending or first observation of an active standalone instance | MilvusUpdated=False with an explicit image/component update reason |
| NIMService v1alpha1 | First observation, Pending, NotReady, or PVC-Created with positive desired replicas | PVC-Created provisioning milestone; generic NotReady is not progress evidence |
| NIMCache v1alpha1 | First observation, Pending, PVC-Created, or Started, excluding completion | PVC/Job creation within that unfinished startup; retained creation history after completion is ignored |
| KServe InferenceService v1beta1 | First observation, unresolved readiness, or model Loading waiting for predictor availability | modelStatus.states.targetModelState=Loading |
| LLMInferenceService v1alpha1 and v1alpha2 | First observation, unknown readiness, or known wait reasons, excluding failed/stopped presets | Ready=False with reason Progressing; gateway or autoscaler wait names alone do not prove a rollout |
| Sandbox v1beta1 | Running intent, Ready=False/DependenciesNotReady, not suspended or finished | That startup state plus current PodScheduled=True |

The 21 rows cover all 23 catalog definitions. Each definition has fresh live
recordings in the validation section below. Those recordings establish
observed status combinations; they do not exercise every predicate branch or
promise an ordered sequence. Constructed regression inputs cover additional
edge cases.

## Scheduling and health limits

PodScheduled on a Pod directly reports its scheduling condition. Sandbox mirrors
that condition. A False condition is distinct from a missing or Unknown one.
The remaining parent mappings require child Pod observations to answer whether
all requested Pods are scheduled. Optional version-specific parent counters are
not treated as a universal scheduling API.

Pending can coexist with Running when some replicas are ready and others are
still needed. Progressing is not a heartbeat: image pull backoff, a blocked
rollout, or a Deployment progress deadline can coexist with it. Keep reasons,
messages, and Failed/Degraded observations visible when deciding whether to act.

NIMService NotReady includes blocked deployments and does not identify the cause.
NIMCache NotReady is a reconcile error, so it does not become normal startup
progress. Existing RayCluster ready state can be sticky, NIMCache InProgress
uses active-Job evidence, and CronJob Running still describes an established
schedule including idle intervals. This change does not turn those existing
Running mappings into proof of live container execution. KServe's existing
Running rule remains specific to its serverless readiness conditions. NIMCache
Failed can appear after one failed Pod, before its Job exhausts retries.

A few existing matches change to make the new combinations truthful: training
Created history no longer adds startup to terminal or running jobs; JobSet
active alone no longer means Running; LWS fallback counts are compared with
desired groups; Deployment Running uses current availability. StatefulSet
serving capacity can coexist with a rollout. Predicates with generation guards
reject observations known to be stale. These guards are not universal: some
existing Running and Failed mappings still use native state alone, and some
guards permit an absent observed generation. Deployment, StatefulSet, and LWS at
zero desired replicas do not claim active execution; other native-ready mappings
retain their operator's readiness semantics.

Grove Progressing reports a controller-observed unfinished operation. Its root
observedGeneration advances after an update converges, so Progressing does not
require that watermark to match the desired generation. The native update
condition does not identify its generation and can describe a superseded
operation. This is not evidence that the latest desired generation is making
progress. Running retains its generation check; a known wait for observation is Pending. OnDelete,
deletion, an explicit updateEndedAt, or a topology failure suppress Progressing.

A known observation wait is not a blanket Undefined fallback. The new branches
require an existing generation, active desired work where applicable, and exact
empty or allowlisted stale status. They exclude deletion, stopped intent, and
known error or terminal signals. Unrecognized native enum values remain
Undefined. Waiting for observation does not add Progressing or establish
whether any Pod is scheduled.

Job readiness is compared with remaining work after successful completions.
A successful replica no longer makes the remaining healthy work Degraded.
JobSet ready counts can include successful Pods in a nonterminal Job, so
Running additionally requires active Jobs. Those aggregate counters still do
not prove active and ready Pod overlap, even within one child Job; child status
is required to answer that question. LWS Available=True no longer bypasses the desired replica count.
Dynamo Running requires a current observation. These checks prevent false
health claims rather than guaranteeing monotonic phase sequences.

## Source basis

The distinction follows the Kubernetes
[Pod lifecycle](https://kubernetes.io/docs/concepts/workloads/pods/pod-lifecycle/)
and [Deployment status](https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#deployment-status).
Native conditions were checked against upstream controllers, including
[LWS](https://github.com/kubernetes-sigs/lws/tree/53466a1dddaae2528dd7357546fa5adf081fef70),
[KubeRay](https://github.com/ray-project/kuberay/tree/fa76e8c19501f13a9c810b85891e81c290353812),
[KServe](https://github.com/kserve/kserve/tree/2797996f1b50bd0687ee25995ae52d9fa1343b79),
[Grove](https://github.com/ai-dynamo/grove/tree/90d6ff4fba7669682da0807d82763b220053e6f5),
and [NIM Operator](https://github.com/NVIDIA/k8s-nim-operator/tree/0aa8bb72040607bf0439793e1df98e141856cace).
The live release behavior was also checked against
[NIM Operator v3.0.2](https://github.com/NVIDIA/k8s-nim-operator/tree/1cc1750182177c4186713f0ac7c4194f836676ee),
[KubeRay v1.6.2](https://github.com/ray-project/kuberay/tree/598eb66aa077c55ae04fa87b192238a3ec184e88),
and [Grove v0.1.0-alpha.14](https://github.com/ai-dynamo/grove/tree/78231d747d1dfe877dadf0bd9c6c261293b245a1).
Newer upstream signals only match when the CR actually contains them.

## Whole-catalog validation (2026-10-19)

The audit examined every STATE frame in all 83 recordings at
[the previous PR head](https://github.com/AviadHayumi/workload-map/tree/ba925769fdbc15f3511be110af34a1205282b0ac/test/e2e/recorded_data),
including recordings that already had no Undefined phases. Applying the updated
definitions to the same 455 objects changes 95 frames in 50 recordings.
Undefined decreases from 76 frames to one. The remaining Undefined observation
is NIMCache NotReady with an explicit retryable reconcile error. It does not
prove either healthy waiting or running capacity, so it is not relabeled
Pending or Degraded.

Twenty of the 23 definitions change. Pod, CronJob, and Sandbox retain their
mappings because the inspected transitions match the available native evidence.
The changes cover:

- Known first/current controller-observation waits across workload families.
- Job remaining-work readiness and genuine failed-replica degradation, including
  failed Pod UIDs reported before the controller increments its failure count.
- JobSet active Jobs in addition to its readiness threshold, which can include
  successful Pods in a nonterminal Job.
- LWS desired ready-group counts despite an old Available condition.
- Grove root update timestamps before child update conditions appear.
- Dynamo current-generation Running and suppression of retained rollout
  markers during deletion.
- Knative child revision/traffic reasons while aggregate readiness is unknown.
- RayService progress across pending-to-active cluster promotion.
- RayCluster observed suspension acknowledgment, and bounded RayJob native New,
  suspension-acknowledgment, and Suspending states.
- PyTorch's observed missing workers alongside a running master, and MPI's
  failed no-retry launcher while another replica remains active.

This comparison isolates mapping changes from watch timing. It does not claim
that a new live run must contain the same intermediate frames. Some earlier
recording endpoints were too weak: an intent-only RayCluster was called
Suspended, and a PyTorch recording stopped while its worker was still missing.
Their checkpoints now require actual controller acknowledgment or the desired
active replicas. Pending-only flows also wait beyond the initial empty status.

Job and JobSet can return from Running to Pending before their parent terminal
condition is written. Several complete root spec/status snapshots are identical
at startup and before termination. A stateless root definition cannot distinguish
those moments. NIMCache can similarly return from InProgress to Pending while
the controller has no active child Job but has not recorded completion. Child
Job/Pod status or richer native status is required to remove that ambiguity.
Dropping Progressing while still Pending is also valid when an observed
operation ends before readiness is satisfied.

The new Job degraded fixture intentionally fails one indexed Pod with retries
remaining while its peer stays ready. The previous fixture demonstrated a
successful completion and incorrectly called the healthy remaining work
Degraded. Native status is never patched to produce the desired phases.

### Fresh recordings

All 83 recordings were replaced by successful real-controller runs on 2026-10-19.
The final corpus has 457 STATE frames across all 23 definitions. Every complete
phase set passes offline SDK replay, and every recording passes result validation.
The runs used isolated two-node kind clusters with Kubernetes v1.34.0 on arm64.
Generated definitions were installed before each flow. Native object events,
recorded phases, and summaries were not rewritten to produce a desired sequence.

| Kind and API version | Operator release | Flows | STATE frames |
| --- | --- | ---: | ---: |
| CronJob batch/v1 | cronjob v1.34.0 | 4 | 5 |
| Deployment apps/v1 | deployment v1.34.0 | 4 | 32 |
| DynamoGraphDeployment nvidia.com/v1alpha1 | dynamo 1.2.1 | 3 | 26 |
| DynamoGraphDeployment nvidia.com/v1beta1 | dynamo 1.2.1 | 3 | 27 |
| InferenceService serving.kserve.io/v1beta1 | kserve v0.20.0 | 2 | 11 |
| Job batch/v1 | batch-job v1.34.0 | 7 | 27 |
| JobSet jobset.x-k8s.io/v1alpha2 | jobset v0.10.1 | 5 | 23 |
| LLMInferenceService serving.kserve.io/v1alpha1 | kserve v0.20.0 | 4 | 13 |
| LLMInferenceService serving.kserve.io/v1alpha2 | kserve v0.20.0 | 4 | 14 |
| LeaderWorkerSet leaderworkerset.x-k8s.io/v1 | lws v0.7.0 | 2 | 14 |
| MPIJob kubeflow.org/v2beta1 | kubeflow v1.9.0+mpiv0.8.2 | 5 | 15 |
| Milvus milvus.io/v1beta1 | milvus 1.3.7 | 2 | 12 |
| NIMCache apps.nvidia.com/v1alpha1 | nim v3.0.2 | 2 | 12 |
| NIMService apps.nvidia.com/v1alpha1 | nim v3.0.2 | 2 | 7 |
| Pod v1 | pod v1.34.0 | 4 | 20 |
| PodCliqueSet grove.io/v1alpha1 | grove v0.1.0-alpha.10-rc1 | 3 | 27 |
| PodCliqueSet grove.io/v1alpha1 | grove v0.1.0-alpha.14 | 4 | 38 |
| PyTorchJob kubeflow.org/v1 | kubeflow v1.9.0+mpiv0.8.2 | 5 | 14 |
| RayCluster ray.io/v1 | kuberay 1.6.2 | 3 | 13 |
| RayJob ray.io/v1 | kuberay 1.6.2 | 5 | 38 |
| RayService ray.io/v1 | kuberay 1.6.2 | 2 | 16 |
| Sandbox agents.x-k8s.io/v1beta1 | agent-sandbox v1.0.5 | 4 | 22 |
| Service serving.knative.dev/v1 | knative knative-v1.15.2 | 2 | 11 |
| StatefulSet apps/v1 | statefulset v1.34.0 | 2 | 20 |

The sole fresh Undefined frame is an explicit retryable NIMCache reconcile error.
The remaining parent-status ambiguities above are preserved. The real Job
degraded flow observes an uncounted failed Pod UID before the numeric failure
counter increments, and already reports Degraded alongside its ready peer.

LLMInferenceService can retain Ready=True for one observation after the stop
annotation changes. That is an acknowledgment delay: the workload still reports
serving readiness until Ready=False/Stopped appears. A stop request alone does
not prove draining or completed suspension. The definition retains Running for
that observation instead of inventing an active transition.

CPU applications and stand-ins exercise controller lifecycles, not GPU execution
or model-inference correctness. Every definition is covered, but not every
conditional branch is necessarily observed in one live run. Source-backed unit
cases cover additional error, deletion, stale-observation, and zero-intent guards.
