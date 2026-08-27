# ADR-011: Preserve revision efficacy attestations, not a quality score

## Status

Accepted as the contract direction. Storage and MCP ingestion remain deferred until a real publisher/evaluation producer is available to validate the shape.

## Context

Skillet can prove which immutable skill revision was distributed, observe truthful downstream lifecycle events, and retain bounded feedback against that exact materialisation. Those signals answer whether a skill was used and what friction or useful behaviour was observed, but they do not establish whether loading the skill changed behaviour relative to the same task without the skill.

A skill can also be valuable without improving generic task completion. Organisation-specific policy, verification, compatibility, or governance guidance may leave goal completion unchanged while materially improving instruction following or preserving an important invariant. Conversely, a model may already know much of a public skill's guidance, so successful execution with the skill does not prove that the skill supplied marginal value.

Skillet therefore needs a way to preserve publisher-generated evaluation evidence without becoming an evaluation runtime, universal benchmark database, or source of a composite quality score.

## Decision

Skillet may accept and expose **revision-bound efficacy attestations** produced by an external trusted publisher or evaluation workflow.

The first supported semantic kind is `marginal_efficacy`: evidence comparing the same evaluation suite under a no-skill condition and with one exact immutable skill revision loaded.

The authoritative evaluation artefacts remain outside Skillet. Skillet preserves only a bounded summary plus a durable evidence reference sufficient to answer:

> Has this exact revision been evaluated, under what conditions, and what behavioural lift was observed?

A representative attestation is:

```yaml
revision_id: rev_...
kind: marginal_efficacy
scope:
  model: claude-sonnet-...
  harness: claude-code
  harness_version: ...
  evaluation_suite: skill-x
  evaluation_suite_revision: ...
  environment_ref: ... # optional; only when materially relevant
summary:
  goal_completion_delta: ...
  instruction_following_delta: ...
  input_token_delta: ...
  duration_delta: ...       # optional
  model_call_delta: ...     # optional
evidence_ref: ...
evaluated_at: ...
```

This is an evidence contract, not a requirement that every producer emit every metric above. `goal_completion_delta` and `instruction_following_delta` remain separate dimensions when present. Producers may preserve additional raw metrics in the referenced external evidence.

### Evidence identity

An efficacy result is comparable only within materially equivalent evaluation conditions. The evidence identity must therefore include:

- the exact immutable Skillet `revision_id`;
- evaluation suite identity and revision/version;
- model identity;
- harness identity and, when available, harness version;
- any environment or tool configuration known to materially affect the result;
- evaluation time and a durable `evidence_ref`.

Unknown values must remain unknown rather than being guessed. Two attestations that differ materially in these conditions are separate observations, not directly interchangeable measurements.

### Three-way evaluation belongs to the producer

A skill-authoring workflow may evaluate:

```text
no skill <-> current revision <-> candidate revision
```

These comparisons answer different questions:

- `current - no_skill`: marginal utility of the current skill;
- `candidate - current`: whether the proposed revision improves on the current one;
- `candidate - no_skill`: resulting marginal utility after the change.

Skillet does not orchestrate this evaluation. Before publication, candidate-vs-current evidence belongs to the authoring/evaluation workflow. Once a candidate becomes an admitted immutable revision, a publisher may attach a `marginal_efficacy` attestation for that revision.

## No composite efficacy score

Skillet must not collapse behavioural dimensions into an `efficacy_score`, `quality_score`, star rating, or similar scalar.

In particular:

- zero goal-completion lift does not mean a governance or organisation-specific skill has no value;
- extra tokens or model calls may be an intentional cost of protecting a high-consequence invariant;
- instruction-following lift and task-completion lift are distinct evidence;
- results are conditional on model, harness, suite, and relevant environment rather than universal properties of the skill.

The existing retrieval philosophy applies: preserve meaningful dimensions independently instead of manufacturing one generic quality number.

## Cost is derived, not core evidence

Provider price is not part of the core attestation contract because tariffs change independently of skill behaviour. Durable resource evidence such as input/output tokens, duration, and model-call counts may be retained when available. Monetary cost can be derived externally using the tariff applicable to the execution time.

## Trust and ranking boundary

An efficacy attestation is evidence, not an instruction or certification.

Initially it must not:

- affect retrieval ranking;
- automatically promote, deprecate, quarantine, or replace a skill;
- authorize execution or additional tool access;
- trigger a source mutation or publication;
- be treated as universally valid outside its recorded evaluation scope.

If efficacy evidence is later considered for retrieval or catalogue policy, that use requires separate evaluation and an explicit decision.

## Implementation sequencing

Do not add a storage table or MCP ingestion surface solely from this ADR. First establish at least one real producer, such as a reviewed publisher CI workflow or `skill-creator` evaluation output, and verify that the bounded contract can represent its evidence without loss or producer-specific leakage.

A later implementation should remain append-only/revision-bound, keep raw evidence external, validate the referenced revision and organisation boundary, and expose attestations as informational catalogue evidence.

## Consequences

Skillet gains a precise direction for answering marginal-value questions without expanding into skill execution or benchmarking. The registry remains the provenance and evidence plane; authoring/evaluation systems remain responsible for producing behavioural evidence.
