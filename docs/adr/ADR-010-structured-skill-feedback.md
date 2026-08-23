# ADR-010: Structured feedback is revision-bound evidence

## Status

Accepted

## Context

Skillet can now observe lifecycle events for an exact materialized skill revision, but lifecycle telemetry only answers whether and how a skill was used. Failures, workarounds, user corrections, ambiguous instructions, compatibility mismatches, and concrete improvement suggestions are different evidence: they explain what went wrong or what a maintainer may want to improve.

Without a bounded feedback primitive, those observations remain trapped in sessions or are converted directly into source changes without durable provenance.

## Decision

Skillet exposes optional `report_skill_feedback` and `list_skill_feedback` MCP tools.

Feedback must reference the same materialization-bound provenance returned by `materialize_skill`. The server validates the immutable revision identity, package digest, organization, and server-recorded materialization request before accepting the observation.

Accepted categories are deliberately small:

- `step_failed`
- `workaround_required`
- `user_correction`
- `ambiguous_instruction`
- `compatibility_mismatch`
- `improvement_suggested`

Each record contains a short summary capped at 1,000 characters plus optional opaque correlation and harness source. Arbitrary transcripts are not part of the contract.

Structured feedback is stored in a dedicated append-only `skill_feedback` table introduced by migration 8. This keeps reviewable product evidence distinct from operational audit events while preserving immutable revision and materialization provenance.

Feedback listing must be scoped to a skill or immutable revision. Results are organization-isolated and ordered newest first.

All adapters may submit explicit feedback because reporting is a deliberate action rather than a lifecycle event inferred by the adapter. Pi offers a convenience command that resolves an active skill to its already-held materialization reference; the other adapters expose the same shared helper operation.

## Trust boundary

Feedback is untrusted evidence. A stored summary is never an instruction to the registry, harness, or maintainer automation.

Recording feedback does not:

- rewrite `SKILL.md`;
- commit or open a pull request;
- create a GitHub issue;
- alter retrieval ranking;
- deprecate or quarantine a revision;
- authorize tool execution;
- require agents to reflect after every use.

Any later workflow that converts feedback into an issue, evaluation case, or proposed source change must remain explicit and reviewable.

## Consequences

Skillet gains a closed-loop evidence primitive without becoming a self-modifying skill runtime. Maintainers can inspect bounded observations against the exact revision that produced them, and future quality workflows can consume those records without scraping conversations.

Lifecycle telemetry and structured feedback deliberately remain separate: lifecycle records whether/how a skill was used; feedback records what friction or improvement evidence was observed.
