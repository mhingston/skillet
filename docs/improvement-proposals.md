# Reviewable improvement proposals

M3.7 turns deterministic improvement evidence into a bounded **review proposal**, not a self-modifying capability. The canonical source repository remains authoritative. Skillet can retain proposal state and verification evidence, but it has no operation that applies a patch, changes the active revision, weakens governance, changes ranking, or merges source.

## Workflow

1. Lifecycle and structured feedback remain bound to an immutable capability revision.
2. `list_improvement_candidates` deterministically derives reviewable evidence candidates.
3. An authorised caller explicitly invokes `prepare_improvement_proposal` (or the equivalent browser action) for one candidate and exact revision.
4. Skillet persists a proposal and emits `skillet.improvement-proposal-handoff/v1` for an external agent or human.
5. The agent/human can return a bounded git patch scoped to the recorded capability source path, an HTTPS external review reference, and machine-readable verification results.
6. Skillet retains every verification attempt. A proposal becomes `ready_for_review` only when every fixed verification obligation is present and passes with exit code 0.
7. Human/source-repository review and merge happen outside Skillet. The normal source ingestion path is the only path that creates a new authoritative immutable revision.
8. If the active source revision changes before proposal completion, the proposal becomes `stale` and further artifact intake fails closed. Skillet never silently rebases a proposal.

`ready_for_review` is intentionally not called accepted, merged, published, or active. It is evidence that the recorded proposed artifact passed the proposal's fixed gates, nothing more.

## Persisted proposal record

Skillet owns the additive `improvement_proposals` sidecar table. Each record contains:

- stable capability identity;
- immutable base revision, commit, tree, package digests, repository and source path;
- deterministic improvement candidate ID and evidence references;
- problem statement and bounded intended outcome;
- the exact deterministic handoff JSON;
- optional bounded git patch and its SHA-256 digest;
- optional HTTPS external review/PR reference;
- append-only verification-attempt history;
- review status (`draft`, `ready_for_review`, `rejected`, or `stale`);
- initiating actor/correlation and timestamps.

Proposal state is deliberately absent from search documents, semantic ranking, capability governance, active revision selection, package identity, and lockfiles.

## Deterministic bounded handoff

The handoff contains only what an external implementation agent needs for one proposal:

- exact base revision/commit/tree/package provenance;
- candidate identity, category/polarity, summary, and exact evidence references;
- bounded source files read from the immutable admitted package rather than the mutable repository checkout;
- fixed Skillet-owned constraints;
- fixed verification obligations.

Source context is capped at 24 regular UTF-8 files, 32 KiB per file, and 128 KiB total. Symlinks/binary content are not included. Secret-looking paths such as `.env`, credential/secret files, private-key formats, and private-key content are excluded. Common inline bearer/token/password forms are redacted before persistence or disclosure. `SKILL.md` must remain safely available or proposal preparation fails.

The handoff marks source and evidence as untrusted data. Feedback text cannot add, remove, or override constraints or verification obligations. In particular, the fixed constraints require an exact immutable base, prohibit weakening eval thresholds/governance or editing unrelated content, and make the return artifact review-only.

## Patch and verification intake

Patch intake is intentionally narrow:

- valid UTF-8 only, maximum 256 KiB;
- git unified diff with at least one `diff --git` header;
- both old and new paths must stay under the capability's recorded source path;
- binary and symlink patches are rejected;
- a different patch cannot replace an already-recorded patch on the same proposal;
- external review references must be HTTPS URLs without embedded credentials.

The v1 fixed verification obligations are:

1. `source_ingestion_validation`: run the normal source-ingestion/capability validation path and record the exact command/tool invocation.
2. `relevant_regression_evals`: run the source repository's existing relevant tests/evals without weakening thresholds and record the exact command/tool invocation.

Results are evidence supplied by the external runner; Skillet records but does not execute those commands. Unknown obligations are rejected, missing obligations keep the proposal in `draft`, and failed/non-zero results remain visible in history. A later passing attempt does not erase an earlier failure.

## Authorization and audit

M3.7 adds explicit authorization actions:

- `proposal.read`
- `proposal.prepare`
- `proposal.attach`
- `proposal.reject`

Preparing additionally requires the existing `evidence.review` and `capability.describe` permissions for the authoritative capability resource. Browser mutations use the existing identity-bound CSRF mechanism. Proposal/resource scope is derived from catalogue/capability policy, never caller-supplied repository coordinates.

Preparation, artifact attachment, and rejection emit local audit events containing actor/proposal/base identity and artifact digest/status where applicable. Raw proposed patch content, source files, credentials, and removed secrets are not copied into audit metadata.

## Agent/MCP surface

- `prepare_improvement_proposal`
- `get_improvement_proposal`
- `list_improvement_proposals`
- `attach_improvement_proposal`

There is deliberately no MCP operation to execute a patch, merge a PR, publish a capability, modify ranking, or mark a proposal authoritative.

## Browser surface

- `GET /ui/catalogue/{revisionID}/proposals` — exact revision candidates and existing proposals.
- `POST /ui/catalogue/{revisionID}/proposals` — explicit prepare action.
- `GET /ui/proposals/{proposalID}` — provenance, handoff, artifact, and verification history.
- `POST /ui/proposals/{proposalID}/artifact` — record bounded artifact/reference and verification evidence.
- `POST /ui/proposals/{proposalID}/reject` — close a proposal without source mutation.

The proposal detail explicitly distinguishes `ready_for_review` from a new active revision and blocks artifact submission once the immutable base is stale.

## Verification

The focused M3.7 E2E proves:

- observed feedback produces a traceable proposal with exact evidence/base provenance;
- source context is bounded and credential-like data is removed/redacted;
- malicious feedback is rendered inert and cannot alter fixed handoff constraints;
- an unauthorised identity cannot inspect or prepare proposal data;
- a scoped patch plus a failed regression result remains `draft` with failure history visible;
- a later complete passing attempt becomes only `ready_for_review`;
- proposal state does not change search/routing order or the active revision;
- normal admission of a distinct upstream revision creates a new immutable revision and makes the prior proposal `stale` rather than rebasing it.

The repository release gate remains:

```sh
go run ./cmd/skillet-verify
```
