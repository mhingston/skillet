# Improver provenance and meta-evaluation (M4.4)

M4.4 adds an opt-in evidence layer for asking a narrower question than capability fitness: **is a particular improvement strategy becoming more productive on a named, versioned distribution?**

It does not make Skillet an autonomous improver. Candidate generation remains external, protected evaluation remains outside the improver's control, and normal proposal/source review remains the promotion boundary.

## Opt in

The MCP surface is disabled by default. Enable it explicitly with:

```text
SKILLET_IMPROVER_META_EVAL=true
```

Enabling M4.4 does not enable the M4.1 experiment, M4.2 lineage, or M4.3 fitness MCP tools. M4.4 may read their append-only records internally when evaluating evidence, but their transport surfaces retain their own feature flags and authorization actions.

## Improver provenance

`record_improver_provenance` binds one immutable provenance record to an exact M4.1 experiment and therefore its exact candidate. It can record:

- agent identity and version;
- harness identity and version;
- model provider, model, and model revision;
- system, prompt, and skill-bundle revisions;
- tool/adaptor names and versions;
- candidate-generation strategy identity;
- candidate-generation strategy configuration digest;
- context and curriculum policy revisions;
- a parent improver strategy when the current strategy was itself derived from earlier work.

Every optional provenance value has an explicit evidence state: `known`, `missing`, or `redacted`. Skillet never fills an absent value by inference. Reading an experiment that has no captured M4.4 provenance returns explicit `missing` states rather than guessed metadata.

Provider/model metadata can therefore be redacted without weakening the trust boundary. Redacted values are intentionally not recoverable from this record.

### Deterministic strategy configuration digest

When `strategy_config_state` is `known`, callers provide bounded JSON. Skillet parses and canonicalizes the JSON and persists only a SHA-256 digest. Semantically equivalent objects with different whitespace or key ordering produce the same digest. Raw strategy configuration is not retained by M4.4.

If the configuration is unavailable or policy-sensitive, callers use `missing` or `redacted`; Skillet does not fabricate a digest.

Provenance is immutable per experiment. An identical repeated capture is idempotent; a conflicting recapture is rejected.

## Provenance is data, not instructions

All provenance fields are evidence strings. They are never executed, appended to protected evaluator instructions, interpreted as policy, or allowed to select promotion criteria. Text such as `ignore held-out evaluation` has exactly the same authority as any other opaque provenance value: none.

This is intentional prompt-injection containment. The improver cannot rewrite the evaluator through metadata it supplies.

## Protected meta-eval definitions

`create_improver_meta_eval` creates a content-addressed, immutable definition containing:

- a human-readable name and explicit version;
- one or more exact development `EvalScope` values;
- one or more exact held-out `EvalScope` values;
- one named metric and direction used to report useful improvement within this meta-eval;
- optional named cost and runtime budget guards.

An `EvalScope` includes exact eval-suite id/version and task-distribution id/version. Results from another scope are rejected rather than pooled.

A `(name, version)` pair is immutable. Reusing it with different development scopes, held-out scopes, metric direction, or budget configuration fails closed. A changed evaluator/distribution therefore requires a new version.

The definition is created under a separate `improver.configure_meta_eval` authorization action. Experiment/result ingestion has no API for editing it.

## Strategy evaluation

`evaluate_improver_strategies` accepts a bounded set of experiment samples. A completed sample binds:

- the exact terminal M4.1 experiment;
- a development M4.3 comparison in a configured development scope;
- a held-out M4.3 comparison in a configured held-out scope;
- optionally, an M4.2 lineage decision representing human/source acceptance or rejection.

The challenger fitness evidence must explicitly reference the sampled experiment. Capability and distribution mismatches fail closed.

Experiments with `failed` terminal status contribute failure evidence without being treated as successful or silently dropped.

### Reported evidence views

For each exact known strategy identity + configuration digest, M4.4 reports separate evidence rather than combining it into one score:

- sample, completed-experiment, and failed-experiment counts;
- accepted, rejected, and undecided candidate counts plus acceptance rate where defined;
- development and held-out `passes_gate` / `fails_gate` / `inconclusive` counts;
- distributions of protected metric gains relative to the comparison champion;
- cost metric distributions per completed experiment;
- runtime metric distributions per completed experiment;
- useful gain within the configured bounded budget;
- useful gain per cost within that budget when the configured cost metric is available;
- budget-compliant, budget-exceeded, and budget-unknown counts;
- a development-pass / held-out-non-pass count and explicit overfit warning.

Missing or redacted strategy identity/config provenance is retained as unattributed evidence and is not declared comparable to known strategies.

## No improver leaderboard

M4.4 deliberately has no universal improver score and no winner field.

Pairwise views say only what can be concluded inside the exact protected meta-eval definition. The normal result is `scoped_evidence_only`: operators inspect the separate evidence dimensions. If provenance is missing/redacted the view is `incomparable_provenance`. If development success fails to hold on protected held-out evidence, the view is `held_out_regression_prevents_superiority_claim`.

This prevents a strategy that overfits development tasks from being labelled superior because it has a high development acceptance rate or cheaper runtime.

Changing the meta-eval/task distribution creates a different comparison context; evidence is not silently pooled across definitions.

## Trust and mutation boundaries

M4.4 is append-only evidence. It cannot:

- start or control an external experiment runner;
- change a capability's active revision;
- modify canonical source or `SKILL.md`;
- mutate semantic search/retrieval ranking;
- change M4.3 promotion criteria;
- edit protected M4.4 meta-eval inputs through result ingestion;
- automatically promote or reject a capability revision;
- let an improver strategy rewrite the evaluator used to judge itself.

Human/source review and existing proposal/lineage boundaries remain authoritative.

## MCP tools and authorization

When enabled, M4.4 exposes:

- `record_improver_provenance` — `improver.record_provenance`;
- `get_improver_provenance` — `improver.read`;
- `create_improver_meta_eval` — `improver.configure_meta_eval`;
- `get_improver_meta_eval` — `improver.read`;
- `evaluate_improver_strategies` — `improver.evaluate`;
- `get_improver_meta_evaluation` — `improver.read`.

Audit events record bounded identifiers and revisions for provenance capture, meta-eval creation, and meta-evaluation execution. Raw strategy configuration and arbitrary provenance bodies are not copied into audit metadata.

## Verification intent

The M4.4 tests cover the issue's safety and evidence boundary directly:

1. two candidate-generation strategies contribute comparable experiment evidence;
2. strategy config digests are deterministic across JSON representation differences;
3. a development-winning strategy that loses on held-out evaluation receives an overfit warning and no superiority claim;
4. missing and redacted provenance remain explicit and safe;
5. malicious provenance text cannot change protected evaluator semantics;
6. changing protected `(name, version)` inputs fails closed;
7. meta-evaluation leaves canonical active/search/ranking state unchanged;
8. the MCP surface remains default-off so earlier milestone behaviour is unchanged unless explicitly enabled.
