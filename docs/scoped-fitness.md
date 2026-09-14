# Scoped fitness and champion/challenger gates

M4.3 adds an opt-in **scoped fitness evidence** domain for comparing exact immutable capability revisions. It deliberately does not create a single global skill score and does not change normal search ranking, activation, governance, version resolution, or canonical source.

Enable only this MCP surface with:

```sh
SKILLET_IMPROVEMENT_FITNESS=true go run ./cmd/skillet -config skillet.yaml
```

The flag is disabled by default and independent from the M4.1 experiment and M4.2 lineage tool flags. The local fitness store may validate references to retained experiment records without exposing experiment creation or executing a runner.

## Evidence scope

Every fitness evidence record is immutable and content-addressed. It binds:

- the exact stable capability and immutable revision, including commit and tree provenance;
- exact eval-suite ID/version;
- exact task-distribution ID/version;
- model, harness, and toolchain identity, plus agent identity when supplied;
- run ID, seed, sample count, and SHA-256 identity of the evaluated sample set;
- separate named `quality`, `regression`, `cost`, `runtime`, and `resource` metrics;
- optional per-metric uncertainty method, confidence level, lower bound, and upper bound;
- optional exact M4.1 experiment IDs, validated to the same capability and eval-suite version;
- bounded provenance/evidence references;
- actor, correlation, and creation metadata.

Metric kind is deliberately categorical. There is no `fitness`, `overall`, weighted, or aggregate metric kind and no API field for a global score. Evidence from different suite or task-distribution versions remains separate and cannot be used in the same comparison.

## Immutable promotion policy

A promotion policy is a separate content-addressed record. It binds:

- one exact configured champion revision;
- one exact eval-suite/task-distribution scope;
- one or more named promotion criteria.

Policy creation has a separate `fitness.configure_policy` authorization action from evidence recording and comparison. A comparison receives only a policy ID and evidence IDs; it cannot submit replacement thresholds with the result.

Supported criterion comparators are:

- `delta_gte` — challenger minus champion must be at least the threshold;
- `delta_lte` — challenger minus champion must be at most the threshold;
- `challenger_gte` — challenger value must be at least the threshold;
- `challenger_lte` — challenger value must be at most the threshold.

Every criterion is an independent AND-gate. Criteria can be marked `protected` for explicit regression evidence, but protected and non-protected criteria are never combined into a weighted score. Consequently a cost/runtime improvement cannot compensate for any failed protected quality/regression criterion.

## Uncertainty and inconclusive results

When a metric includes uncertainty, Skillet evaluates the conservative interval rather than only the point estimate. For a delta criterion, the comparison interval is computed from both champion and challenger bounds. A criterion:

- passes only when the whole relevant interval satisfies its threshold;
- fails only when the whole interval is on the failing side;
- is `inconclusive` when the interval crosses the threshold.

`minimum_confidence` can require uncertainty evidence at or above a configured confidence level. Missing required metrics, missing required uncertainty, or insufficient confidence is `inconclusive`, never success.

The comparison decision is exactly one of:

- `passes_gate` — every named criterion passes;
- `fails_gate` — at least one named criterion fails;
- `inconclusive` — none fail, but at least one cannot be concluded.

There is no auto-promotion path. `passes_gate` is evidence for human/source review only. It does not record a M4.2 lineage promotion decision, activate a revision, mutate source, or change retrieval ranking. A reviewer may separately use the immutable comparison ID as a decision reference in the normal review/lineage process.

## MCP surface

When `SKILLET_IMPROVEMENT_FITNESS=true`:

- `record_fitness_evidence` records immutable revision-scoped evidence;
- `get_fitness_evidence` reads one authorised evidence record;
- `list_revision_fitness_evidence` lists bounded evidence for one exact revision;
- `create_fitness_promotion_policy` freezes one champion/scope/criteria policy;
- `get_fitness_promotion_policy` reads an immutable policy;
- `compare_fitness_evidence` produces deterministic criterion-level gate evidence;
- `get_fitness_comparison` reads an immutable comparison result.

Authorization actions are separately exposed as `fitness.read`, `fitness.record`, `fitness.configure_policy`, and `fitness.compare`.

## Verification

Focused and end-to-end tests prove:

- the tool surface is absent by default and appears only when explicitly enabled;
- exact revision, eval-suite, task-distribution, executor, run/seed/sample, metric, uncertainty, and provenance bindings are retained;
- a clearly better challenger passes a configured gate;
- quality improvement plus a protected regression fails rather than being compensated by lower cost/runtime;
- a small/noisy interval crossing a threshold is reported as inconclusive;
- suite/task-distribution mismatch fails closed instead of pooling evidence;
- cost/runtime evidence remains separate from quality evidence;
- immutable policy thresholds change identity rather than mutating history;
- comparison results do not change active/searchable canonical state.

The existing repository-wide verification remains the regression gate for M1-M3 and earlier M4 slices:

```sh
go run ./cmd/skillet-verify
```
