# Curriculum and capability-gap evidence (M4.5)

M4.5 adds an opt-in evidence layer for answering a bounded question: **given repeated failures or weak coverage in one named task distribution and environment, what should we practise, document, or evaluate next without changing the evaluator that judged the current experiment?**

It does not make Skillet a task generator or training runner. Generated task bodies and eval cases remain external immutable artifacts, candidate generation remains external, and protected eval adoption remains an explicit review/versioning step.

## Opt in

The MCP surface is disabled by default:

```text
SKILLET_IMPROVEMENT_CURRICULUM=true
```

Enabling M4.5 does not enable the M4.1-M4.4 MCP surfaces; each retains its own feature flag and authorization actions.

## Capability gaps are scoped evidence

`record_capability_gap` records a content-addressed snapshot bound to:

- an exact capability revision;
- a named task-distribution id and version;
- an explicit environment;
- a stable failure/coverage cluster key;
- bounded evidence references.

Supported evidence kinds are lifecycle failures, feedback failures, weak eval cases, coverage gaps, compatibility failures, and effective-pattern evidence. Failure-like evidence requires at least two observations before it becomes a gap; coverage/effective-pattern evidence can describe a single explicit hole or reusable opportunity.

Feedback is always normalized to `untrusted` regardless of what the caller supplies. Its summary is inert evidence text. It cannot set thresholds, change task classification, approve a proposal, or alter an eval suite.

M4.5 does not feed capability gaps into semantic retrieval ranking.

## Versioned curriculum proposals

`create_curriculum_proposal` creates an immutable `(name, version)` review artifact motivated by one gap. Kinds are deliberately structural:

- `training_task` → development;
- `development_eval` → development;
- `protected_eval` → held-out;
- `capability_guidance` → development.

The caller cannot separately supply the audience, so malicious evidence text cannot relabel a held-out proposal as development content.

Proposal content is stored externally by reference plus SHA-256 digest. Skillet does not execute it. Every proposal must declare either:

- a deterministic/verifiable oracle reference plus digest; or
- an explicit `review_required` path.

`review_curriculum_proposal` records one immutable accept/reject decision. Changing the decision requires a new proposal version rather than rewriting historical review evidence.

## Protected suite evolution

`create_curriculum_eval_suite_version` creates an immutable suite version from explicitly accepted proposal versions. Development entries must be accepted `development_eval` proposals; held-out entries must be accepted `protected_eval` proposals.

A `(suite name, version)` pair is immutable. Reusing it with different content fails closed. Evolving `v7` therefore creates `v8`; it never edits `v7` in place. When a parent version is supplied, parent cases cannot silently disappear. This conservative rule forces potentially evaluator-changing work through additional reviewed proposal versions rather than allowing an unnoticed case deletion.

M4.1 experiments already store exact eval-suite ids/versions in their immutable specification and result evidence. Creating a later M4.5 suite version does not rewrite those historical bindings, promotion thresholds, or result records.

Adopting a newly created suite version into a future experiment remains an explicit caller decision outside M4.5.

## Held-out isolation from candidate generation

`prepare_curriculum_handoff` accepts explicit reviewed proposal ids and produces candidate-generation input only from accepted non-held-out proposals. `protected_eval` / held-out proposals are omitted structurally: their title, intent, artifact reference, artifact digest, and oracle metadata are not present in the handoff. The response contains only an excluded-held-out count.

There is no override that exposes an existing held-out proposal. If policy later decides material is safe for development use, create and review a distinct non-held-out proposal version. This keeps the classification decision explicit and auditable.

## Trust and mutation boundaries

M4.5 cannot:

- execute generated tasks, eval cases, scripts, or training jobs;
- treat user feedback or proposal prose as evaluator policy;
- mutate an existing protected eval-suite version;
- rewrite historical experiment suite/version bindings;
- change M4.3 promotion thresholds;
- promote or activate a capability revision;
- modify canonical source or `SKILL.md`;
- alter semantic search or retrieval ranking;
- leak held-out proposal content through the candidate-generation handoff.

## MCP tools and authorization

When enabled, M4.5 exposes `record_capability_gap`, `get_capability_gap`, `create_curriculum_proposal`, `get_curriculum_proposal`, `review_curriculum_proposal`, `get_curriculum_review`, `create_curriculum_eval_suite_version`, `get_curriculum_eval_suite_version`, and `prepare_curriculum_handoff`.

They use separate `curriculum.*` authorization actions for reading, recording gaps, preparing proposals, reviewing proposals, evolving suites, and preparing candidate handoffs. Audit metadata records bounded ids, versions, classifications, and counts rather than copying arbitrary proposal/evidence bodies.

## Verification intent

The M4.5 tests cover the issue boundary directly:

1. repeated failures aggregate into an explicitly scoped gap and feedback trust is forced to untrusted;
2. a development eval/task proposal is immutable, versioned, and explicitly reviewed;
3. adding a protected case to an existing suite version fails and succeeds only as a new version;
4. held-out proposal content is absent from candidate-generation handoffs;
5. a completed M4.1 experiment remains bound to the original suite version after suite evolution;
6. malicious feedback text cannot change proposal audience, review state, or evaluator configuration;
7. curriculum activity leaves canonical active/search/ranking state unchanged;
8. the MCP surface is default-off, preserving previous milestone behaviour unless explicitly enabled.
