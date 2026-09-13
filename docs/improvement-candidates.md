# Evidence-backed improvement candidates

Skillet derives reviewable improvement candidates from evidence already accepted by the revision-bound lifecycle and structured-feedback APIs. Candidates are a read model: they are not persisted and deriving or reading them does not change source repositories, capability ranking, governance state, package history, or lockfiles.

## Maintainer/agent read surface

The Skillet MCP server exposes `list_improvement_candidates` when the catalogue is configured.

Inputs:

- `revision_id`: preferred when reviewing one immutable revision;
- `skill_id`: derives across retained active/superseded/removed revisions for one stable skill-backed capability;
- `limit`: 1-50, default 25;
- `offset`: deterministic pagination offset.

At least one of `skill_id` or `revision_id` is required. If both are supplied, the revision must belong to that stable capability. Organisation isolation is enforced by resolving revisions inside the authenticated organisation.

The result contains deterministic candidates, duplicate counts, evidence-window counts, explicit truncation state, and inert `github_issue_draft` handoff data. The handoff is data only: Skillet does not call GitHub or another source-control system from this workflow.

## Candidate categories

Candidate derivation deliberately uses the existing bounded evidence vocabulary rather than inventing a quality score.

| Evidence signal | Candidate category | Polarity |
| --- | --- | --- |
| lifecycle `failed` | `lifecycle_failure` | friction |
| `step_failed` | `step_failure` | friction |
| `workaround_required` | `workaround_required` | friction |
| `user_correction` | `user_correction` | friction |
| `ambiguous_instruction` | `ambiguous_instruction` | friction |
| `compatibility_mismatch` | `compatibility_mismatch` | friction |
| `improvement_suggested` | `improvement_suggested` | friction |
| `effective_pattern` | `effective_reusable_pattern` | positive |

Activation, deactivation, and ordinary completion do not create positive candidates. Positive evidence requires an explicit `effective_pattern` observation. This keeps ordinary success from being silently reinterpreted as quality evidence.

## Provenance and trust

Every candidate is bound to a stable skill-backed capability and one immutable revision. Candidate provenance includes revision ID, commit, tree, and retained package digests. Every source observation is exposed as a bounded evidence reference containing its persisted record/audit ID, signal, materialisation ID, package digest, correlation ID/source when present, timestamp, and (for feedback) a bounded excerpt plus SHA-256 of the complete stored summary.

Derivation rejects evidence whose capability, revision, commit, tree, package digest, or materialisation identity does not agree with the immutable revision provenance. Existing lifecycle and feedback writes remain the authority for accepting materialisation-bound evidence; this feature does not create a second provenance authority.

Feedback summaries are untrusted data. Candidate summaries are deterministic text generated from bounded categories and counts, not instructions inferred from the feedback body. Handoff payloads label source summaries as untrusted and explicitly state that they do not authorize execution or mutation.

## Dedupe/correlation policy

Dedupe is intentionally conservative and deterministic:

1. observations are considered for dedupe only when `correlation_id` is non-empty;
2. two observations collapse only when evidence kind, signal, correlation ID, and a case/whitespace-normalized feedback summary are equal;
3. source/adapter name is not part of the key, so the same correlated observation copied by two adapters does not inflate evidence;
4. observations without a correlation ID are never deduped merely because their text is equal;
5. lifecycle failures and feedback categories remain separate signals rather than being merged into an opaque score.

`duplicates_ignored` makes collapsed observations visible to the reviewer. `signal_count` counts distinct observations after this policy is applied.

## Contradictory evidence

Friction and positive evidence are never averaged together. If an immutable revision has at least one friction candidate and at least one explicit effective-pattern candidate, every candidate for that revision sets `contradictory_evidence_present=true`. Both sides remain independently inspectable with their own source references.

## Bounds and determinism

Stable-capability reads are bounded to 50 retained revisions; callers should select a `revision_id` for deeper history. For each revision, derivation considers at most the newest 100 structured-feedback records and newest 100 lifecycle records. If more evidence exists, `source_evidence_truncated=true` is returned rather than silently implying a complete history.

Within the selected evidence window, ordering, dedupe, candidate IDs, summaries, evidence references, and handoff payloads are deterministic. Candidate IDs are content-derived from stable capability, immutable revision, category/polarity, and ordered persisted evidence references. A golden fixture protects the serialized candidate/handoff contract.

## Explicit non-goals

This M1 loop does not:

- generate or apply source edits;
- open GitHub issues or pull requests automatically;
- execute a proposed fix;
- change capability/skill ranking;
- infer or mutate governance lifecycle state;
- update lockfiles or retained packages;
- assign an overall quality score.

A human or separately authorized external workflow may choose to consume the deterministic handoff payload after reviewing the underlying evidence.
