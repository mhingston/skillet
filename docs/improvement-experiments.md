# Opt-in improvement experiments

M4.1 adds a bounded **improvement experiment control plane** on top of the M3 proposal workflow. Skillet can issue an immutable, reproducible experiment specification and later retain result evidence, but it does not run agents, execute shell commands, train models, start workers, or apply source changes.

The feature is deliberately disabled by default. Enable the M4.1 MCP surface explicitly for a Skillet process with:

```sh
SKILLET_IMPROVEMENT_EXPERIMENTS=true go run ./cmd/skillet -config skillet.yaml
```

If the variable is absent, empty, or anything other than `true` (case-insensitive), no experiment MCP tools are registered. Existing M1-M3 deployments therefore require no new services or workers and retain their existing behaviour.

## Workflow

1. Evidence remains bound to an immutable capability revision and is turned into an M3 improvement proposal.
2. The proposal must reach `ready_for_review`; draft, rejected, or stale proposals cannot become experiments.
3. An authorised caller explicitly invokes `create_improvement_experiment` with a bounded hypothesis, executor identity metadata, a versioned protected eval suite, and optional execution budgets.
4. Skillet canonicalises the specification, hashes it, and emits `skillet.improvement-experiment-handoff/v1` with a content-addressed `spec_revision` and handoff SHA-256.
5. An external runner may consume that data and execute the experiment under its own runtime/credential/authorization boundary.
6. The runner returns raw protected-eval measurements plus bounded artifact/evidence references through `submit_improvement_experiment_result`.
7. Skillet verifies the exact experiment/spec/handoff/eval-suite binding and computes pass/fail from the immutable protected thresholds recorded at issuance.
8. Result state remains additive evidence. Canonical source, active revision, governance, retrieval ranking, packages, and locks are unchanged until the normal human-reviewed source workflow creates and ingests a new revision.

Changing any issued hypothesis, outcome, executor identity, eval suite/version/threshold, budget, or proposal provenance creates a different content-addressed spec and therefore a different experiment identity. Historical experiment records are never edited into a different experiment.

## Canonical specification

`skillet.improvement-experiment-spec/v1` binds:

- stable capability identity;
- exact base revision, commit, tree, package digests, repository ID, and source path;
- originating M3 proposal and candidate IDs;
- compact immutable evidence references and a proposal snapshot digest;
- the proposal patch SHA-256 and, only when safe, a credential-free external review reference;
- a bounded falsifiable hypothesis and intended measurable outcome;
- agent/model/harness/toolchain identity strings when known;
- eval suite ID/version and immutable protected metrics, comparators, and thresholds;
- declared cost/runtime/token budgets;
- fixed control-plane obligations stating that execution and canonical mutation remain external.

The experiment handoff is reference-based by design. It does **not** copy proposal patch bodies, source-file contents, feedback excerpts, repository credentials, bearer tokens, signed URLs, or generic executable commands into the experiment record.

## Protected evaluation

Protected evals use a simple v1 contract:

- `name` — stable eval identifier;
- `metric` — metric identity;
- `comparator` — `gte` or `lte`;
- `threshold` — finite immutable threshold.

Result intake accepts only the raw `name` + numeric `value` (and optional evidence reference). It has no threshold field. Skillet looks up the issued protected eval and evaluates the value against the original comparator/threshold itself. Missing, duplicate, invented, mismatched-suite, or non-finite measurements fail closed.

A runner-level failure may instead return a bounded `failure_reason` with no protected measurements. Such an experiment becomes `failed`; the failure remains evidence and does not mutate the issued spec.

## Result references and secret boundary

Result artifacts and per-measurement evidence may use only:

- credential-free `https://` references with no userinfo, query string, or fragment; or
- `sha256:<64-lowercase-hex>` content references.

Artifact count and all free-text/identity fields are bounded. Common private-key, bearer, API-key, access-token, client-secret, and password assignment forms are rejected from handoff/result metadata. Runtime secrets belong in the external runner and are never part of the Skillet experiment specification.

## Status and stale-base behaviour

M4.1 uses the smallest useful state machine:

- `issued` — immutable handoff exists and can accept one bound result;
- `completed` — all protected eval measurements were present and passed;
- `failed` — a protected gate failed or the runner reported an execution failure;
- `cancelled` — the Skillet control-plane record was cancelled; this does not control an external runner;
- `stale` — the capability's active revision changed before result intake.

Only `issued` experiments are rechecked for base staleness. Terminal completed/failed/cancelled records remain stable historical evidence after later source revisions are admitted. A stale experiment rejects result intake rather than rebasing.

## Authorization and audit

The experiment domain defines separate authorization actions:

- `experiment.read`
- `experiment.create`
- `experiment.submit_result`
- `experiment.cancel`

Creation also requires read access to the originating proposal. Capability/resource scope is derived from the catalogue and proposal provenance, not caller-supplied repository coordinates.

Issuance, accepted result intake, and cancellation emit local audit events containing bounded actor/capability/revision/proposal/experiment/spec identifiers and result/handoff digests. Raw patch/source content and runner credentials are not copied into audit metadata.

## MCP surface

When explicitly enabled:

- `create_improvement_experiment`
- `get_improvement_experiment`
- `list_improvement_experiments`
- `submit_improvement_experiment_result`
- `cancel_improvement_experiment`

There is intentionally no `run_experiment`, shell/tool execution, model-training, source-apply, rank-update, governance-update, or worker endpoint.

## Verification

Focused tests prove:

- experiment tools are absent by default and appear only after explicit opt-in;
- a ready M3 proposal produces exact revision/proposal/candidate/evidence provenance;
- canonical spec/handoff identity is deterministic and changes create a new experiment;
- proposal source, patch body, evidence excerpt secrets, and signed/query-bearing references do not leak into the handoff;
- result/spec/handoff/eval-suite mismatches fail closed;
- protected thresholds cannot be supplied or weakened by result intake;
- stale-base result intake is rejected without rebasing;
- completion leaves active revision/searchability/ownership unchanged;
- issuance and accepted result intake are audited;
- the existing aggregate M1-M3 verification remains the repository-wide regression gate:

```sh
go run ./cmd/skillet-verify
```
