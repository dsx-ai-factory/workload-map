<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# recorder

Drives a workload on a live cluster through a declared flow and records every distinct CR
it passes through into a YAML fixture. State is judged from the workload's own fields,
never from Karta, so the offline replay can feed the fixture through Karta and check it
reads each state the same way. The recorder source pulls in no test framework; failures
come back as errors. The unit tests are Ginkgo, like the rest of the repo.

## Files

- doc.go: the package comment.
- recorder.go: setup and engine. Cluster, Config, Fixture, Recorder, New, AddState,
  SetTimeout; Run drives a flow, Save writes the recording.
- flow.go: the authoring API. NewFlow, Through, and the Step builders (Reaches, Optional,
  With, Do), plus the state and action vocabulary (StateCheck, classify, Action).
- observation.go: one live run. The watch loop (watchAndAct, startWatch), recording each frame
  (record, keep), and step actions (advanceStep, performAction). A watch that cannot
  resume fails the run.
- cr.go: helpers over an unstructured CR. stripVolatileFields, hasObservedCurrentGeneration,
  blankWithGVK, dumpStatus.
- order.go: the invariant. validateObservedOrder checks the observed states are a legal
  walk of the declared journey.
- recording.go: the on-disk format (Recording, Event, RecordedAction) and the Reader that
  walks a saved recording back for the replay.
- suite_test.go, recorder_internal_test.go, recording_internal_test.go: offline Ginkgo
  unit tests, no cluster.

## Flow of a run

```go
out, err := recorder.NewFlow(rec, "scaled", "testdata/deployment/running.yaml").Through(
    recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(1)).Do(ScaleReplicas(3)),
    recorder.Reaches(kartav1alpha1.RunningStatus).With(ReplicasReady(3)),
).Run(ctx)
```

1. Run creates the workload from the manifest (recorder.go).
2. observe opens a watch from the create resourceVersion, so no event between Create and
   the watch attaching is missed (observation.go).
3. Every distinct frame is kept, volatile metadata stripped. A frame whose controller has
   not observed the spec yet (observedGeneration < generation) is kept marked
   staleObservedGeneration.
4. Once an observed frame reaches the next declared step, its action is performed; the run
   ends at the declared terminal state or on timeout.
5. validateObservedOrder checks the observed states walked the declared journey (order.go).
6. Save stamps the Fixture and writes the recording, passed or failed, under
   outputDir/operator/version/kartaName/flow.yaml (recording.go).

The replay opens the file with OpenRecording and walks the STATE events back through
Karta, asserting each state matches.

## Entities

- Recorder: per workload type. Cluster access, the state predicates, the timeout.
- Flow: one journey to record. A workload manifest plus the ordered steps it must reach.
- Fixture: the catalog identity a recording is saved under.
- Recording: the on-disk result. A STATE event per distinct CR, an ACTION event per patch.
- Reader: walks a saved Recording back for the replay tests.

## Run

```sh
go test ./...            # offline unit tests, no cluster
make test-lib            # from the repo root, runs the recorder unit tests too
make test-replay         # from the repo root, replays the recorded fixtures through Karta
make record-e2e          # from the repo root, records the flows against a live cluster
```
