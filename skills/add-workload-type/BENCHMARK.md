<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- Copyright (c) 2026 NVIDIA Corporation -->

# Skill Benchmark: add-workload-type

Model: claude-opus-5
Date: 2026-08-18

> These numbers describe the 2026-08-18 skill. The skill was refreshed on
> 2026-09-22 and has not been re-measured since. The refresh added the step 0
> branch between a standalone definition and a built-in contribution, pointed
> step 7 at the recorded fixtures under `test/e2e/recorded_data/`, and rewrote
> step 5 around state coverage. Two assertions in `evals/evals.json` were
> reworded for the branch, so the pass rates below are not directly comparable
> to a future run. A second pass the same day made the skill standalone (no
> assumption of a Karta checkout, `karta validate` as the primary validator),
> which reworded the harness assertions again. Read this as the baseline to beat.
Evals: 1, 2, 3 from `evals/evals.json` (3 runs each per configuration, 18 runs total)

Each eval prompt was run by an agent with the skill available and by a baseline
agent without it. Both configurations had full repository access. Every produced
definition was checked with `hack/karta-verify` and by querying the produced YAML
for the asserted paths, rather than by inspection.

## Summary

| Metric | With Skill | Without Skill | Delta |
|--------|------------|---------------|-------|
| Pass Rate | 97% +/- 8% | 97% +/- 8% | +0.00 |
| Time | 284.8s +/- 258.7s | 428.4s +/- 468.4s | -143.6s |
| Tokens | 56410 +/- 19981 | 81135 +/- 49419 | -24724 |

## Revised assertions

The expectations in `evals/evals.json` were rewritten after this run, because the
original set could not tell the two configurations apart. The same 18 run
artifacts were then graded again against the revised set. No agent was re-run, so
the time and token figures above are unchanged and only the pass rates move.

| Assertion set | With Skill | Without Skill | Delta |
|---------------|------------|---------------|-------|
| Original | 97% | 97% | +0.00 |
| Revised, evals 1-3 | 96% | 51% | +0.44 |
| Revised, discriminating only, eval 1 | 93% | 7% | +0.87 |
| Revised, discriminating only, eval 2 | 100% | 22% | +0.78 |

What separates the configurations, measured as with_skill/without_skill over 3
runs each:

| Behaviour | Eval 1 | Eval 2 |
|-----------|--------|--------|
| Deliverable is a definition only, no Go source or e2e module | 3/3 vs 0/3 | n/a |
| Predictions written before the run, exercised with `--strict` | 3/3 vs 0/3 | 3/3 vs 0/3 |
| Validated with `hack/karta-verify` rather than a bespoke harness | 3/3 vs 1/3 | 3/3 vs 0/3 |
| Suspended Workflow resolves Suspended, not Running | 2/3 vs 0/3 | n/a |
| States which parts remain unproven | n/a | 3/3 vs 0/3 |

The suspend guard is the one pure correctness discriminator. All three baseline
runs mapped `running` to a bare `byPhase: Running`, which misreports a suspended
workflow, because Argo leaves `.status.phase` at Running while `.spec.suspend` is
true. Two of the three skill-guided runs guarded the rule against `.spec.suspend`.

The rest are verification discipline rather than output quality, and that
distinction is worth keeping in view: they measure whether the run followed the
skill's steps 6 and 7. They are not circular, because following those steps
changed outcomes. Both skill-guided eval 1 runs discovered through the
predict-then-run step that an Argo Workflow needs a multi-instance component,
after a single-instance draft failed with `instance ids count (1) does not match
results count (2)`. Neither reasoned it out in advance.

Eval 4 was added to `evals.json` for Volcano Job, which is absent from
`docs/catalog/`, to replace the content-discrimination role eval 2 can no longer
serve. It has not been run, and its expectations carry no measured counts.

## Per-eval breakdown

| Eval | Config | Pass rate | Tokens | Time |
|------|--------|-----------|--------|------|
| 1 Argo Workflow, authored from scratch | with skill | 0.92 | 80847 | 617s |
| 1 Argo Workflow, authored from scratch | baseline | 0.92 | 147265 | 1006s |
| 2 batch/v1 CronJob | with skill | 1.00 | 54791 | 176s |
| 2 batch/v1 CronJob | baseline | 1.00 | 62038 | 218s |
| 3 Quickstart question, boundary case | with skill | 1.00 | 33594 | 61s |
| 3 Quickstart question, boundary case | baseline | 1.00 | 34101 | 61s |

## Notes

The pass-rate delta was zero under the assertions in force when this run
executed. Those assertions were rewritten afterward, and the revised set scores
the same artifacts at +0.44. See the revised assertions section above. The
summary table reports the original grading, so it understates the difference.

Why the original assertions failed to discriminate. The validator passed on all
13 definitions produced across both configurations, so "passes the KartaValidator"
cannot separate them. The same holds for full GVK, presence of a root
statusDefinition, one spec pattern per component, and the nested
`.spec.jobTemplate.spec.template` path. Baselines got all of these right unaided.
These are correctness minimums, and they are retained in `evals.json` marked as
floor expectations so a regression still fails, but they carry no signal about
the skill.

Cost runs the other way from the usual expectation. The skill made the work
cheaper, not more expensive. On eval 1 the baseline used 82 percent more tokens
and 63 percent more wall-clock. The cause is scope: both baseline runs that
finished eval 1 expanded the task into a typed Go catalog constructor under
`pkg/catalog/kartas/`, an `hack/e2e` operator module, and a landing checklist,
none of which the prompt asked for. The skill-guided runs produced a definition
and stopped. Bounding scope is the clearest measured effect in this run.

Eval 2 is contaminated. The repository already ships
`docs/catalog/batch-cronjob-v1.yaml`. Both configurations found it, so eval 2
tests whether an agent can locate an existing catalog entry, not whether it can
author a definition. Both scored 1.00. Replace it with a workload absent from
`docs/catalog/` before treating its result as meaningful.

Eval 3 shows no over-triggering. All 3 skill-available runs declined to use the
skill, recorded `SKILL_USED: no`, answered the question, and authored no
definition. Triggering was correct in both directions across the set: the skill
fired on evals 1 and 2 without being named, and stayed out of eval 3.

Variance is high and the run count is low. Three runs per configuration with a
population stddev near 468s on baseline time is not enough to call the time and
token deltas precisely. The direction is consistent across every eval; the
magnitude is not settled.

The one quality difference the assertions missed. Both skill-guided eval 1 runs
discovered through step 7 that an Argo Workflow needs a multi-instance component,
after a single-instance draft failed with `instance ids count (1) does not match
results count (2)`. Neither reasoned this out in advance. That is the predict-then-run
step doing real work, and no current assertion credits it.

## Environment note

Every skill-guided run that reached validation reported that the step 6 command
failed and worked around it with `-mod=readonly`:

```text
go: inconsistent vendoring in <repo>:
	github.com/onsi/ginkgo/v2@v2.32.1: is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt
```

This was a stale local `vendor/` directory on the machine that ran the benchmark,
not a defect in the skill or the repository. `vendor/` is gitignored and is not
referenced by the Makefile or CI, so a dependency bump in go.mod left it behind
and Go's automatic vendor mode then failed every command. Removing the directory
resolved it. The step 6 command in `SKILL.md` is correct as written.

The measured effect is still worth noting: an unrelated broken toolchain cost
each run several tool calls to diagnose, which inflates the token and time
figures above for both configurations.

## Reproducing

1. Run the eval 1, 2 and 3 prompts from `evals/evals.json` with and without the
   skill available, 3 runs each. Eval 4 is documented but was not run and is not
   part of these figures.
2. Grade each run against its `expectations` list.
3. Validate every produced definition, and exercise it against a real CR with
   predictions written before the run:

   ```sh
   go run ./hack/karta-verify --karta <definition> \
     --workload <cr> --predict <predictions> --strict
   ```

   `--karta` alone only validates the definition. `--workload`, `--predict` and
   `--strict` are what make the predict-then-run discriminators observable;
   without them a run cannot fail the way the benchmark records.
4. Confirm each asserted path resolves in the produced YAML, rather than reading
   it by eye.
5. Aggregate the per-run results into this summary.

## Run 2: 2026-09-22, refreshed skill vs the version it replaced

Model: claude-opus-5
Evals: 1-4 from `evals/evals.json`, one run per cell (8 runs total)
Baseline: the skill at commit `889b50e`, i.e. the version this refresh replaced.
Not "no skill", so this measures the refresh, not the skill.

| Configuration | Pass rate | Discriminating | Time | Tokens |
|---|---|---|---|---|
| Refreshed | 100.0% (21/21) | 11/11 | 933s | 112,770 |
| Previous | 84.5% (17/21) | 8/11 | 1651s | 117,075 |
| Delta | +15.5 pts | +3 | -718s | -4,305 |

Per eval:

| Eval | Refreshed | Previous |
|---|---|---|
| 1 Argo Workflow | 7/7 | 5/7 |
| 2 CronJob | 5/5 | 5/5 |
| 3 quickstart guard | 3/3 | 3/3 |
| 4 Volcano Job | 6/6 | 4/6 |

### What actually moved

The entire delta is one behaviour: the step 0 branch between a standalone
definition and a built-in contribution. Both prompts describe a workload running
on the user's own cluster, which is the standalone signal. Both previous-version
runs instead produced Go source for `pkg/catalog/kartas/` plus a catalog
registration - eval 4's went as far as a `git apply` patch touching
`catalog.go`, the generated YAML and the README. That is a substantially larger
change than either prompt asked for. The refreshed runs read the signal and
stayed standalone; eval 4's offered the built-in path as a question instead.

### What no longer discriminates, and why

The suspend trap in eval 1 passed in both configurations. In the 2026-08-18 run
it was 2/3 vs 0/3, but that compared skill against no skill; the previous
version already carried the suspend guidance, so the assertion now measures a
floor both clear rather than a difference.

The step 7 root-component correction did not discriminate either. The eval 2
previous-version run discovered the harness blind spot unaided: it hit the
mismatch, traced it to `pkg/tree/tree.go`, reproduced it against a shipped
catalog file as a control, and wrote its own probe. The correction documents a
real defect, verified independently, but on this eval set it saves a detour
rather than preventing a wrong answer.

Eval 2 behaved exactly as its own `purpose` field predicts: no discrimination,
because the answer ships in the repo and both configurations verify properly.
Eval 3 is a guard and was flat by design; neither configuration consulted the
skill for a read-only question.

### Limits of this run

One run per cell. The +/-18.0 spread quoted for the previous version is across
evals, not across repeats, so nothing here separates skill effect from run-to-run
variance. The 2026-08-18 run used three runs per cell; this one does not, and its
timing and token figures should be read as anecdotes. The eval 4 assertion
requiring a null-safe replica default is arguably mis-specified: the
previous-version run omitted the default deliberately and preserved index
alignment, which is defensible, and it was graded as a failure on the literal
text.

Assertion wording changed between runs, so these pass rates are not comparable
with Run 1's.
