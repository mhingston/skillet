# M4 capability-evolution release gate

M4 is an opt-in evidence and control-plane layer on top of the preserved M1-M3 product boundary. It adds reproducible improvement experiments, append-only lineage, scoped fitness evidence, improver provenance/meta-evaluation, curriculum-gap evidence and an authenticated external-runner protocol. It does **not** turn Skillet into an agent harness, workflow engine, model trainer, source editor or automatic promotion authority.

`go run ./cmd/skillet-verify` is the single deterministic release entry point. In addition to the existing M1 Journeys A-E, M2 enterprise acceptance and M3 Journeys F-K, it now runs `TestM47IntegratedCapabilityEvolutionAcceptance` and writes:

- `artifacts/verification/m4-integrated.json` — raw evidence emitted by the integrated M4 fixture;
- `artifacts/verification/m4-acceptance.json` — normalized M4 release evidence, including preserved M1-M3 status;
- `artifacts/verification/verification.json` — the top-level M1-M4 gate and report references.

## Enablement and authorization

Every M4 transport surface remains disabled unless its feature flag is explicitly set to `true`:

| Slice | Flag |
| --- | --- |
| M4.1 experiments | `SKILLET_IMPROVEMENT_EXPERIMENTS` |
| M4.2 lineage | `SKILLET_IMPROVEMENT_LINEAGE` |
| M4.3 scoped fitness | `SKILLET_IMPROVEMENT_FITNESS` |
| M4.4 improver meta-evaluation | `SKILLET_IMPROVER_META_EVAL` |
| M4.5 curriculum gaps | `SKILLET_IMPROVEMENT_CURRICULUM` |
| M4.6 external runners | `SKILLET_EXTERNAL_IMPROVEMENT_RUNNERS` plus M4.1 experiments |

Flags expose eligible tools; they do not grant authority. The normal MCP authentication/authorization path still applies, and each M4 domain retains its own action checks. Production deployments should use the existing static or OIDC identity model rather than development mode.

The release fixture proves that with every M4 flag absent, representative M4 tools are not discoverable and ordinary skill discovery plus canonical state remain unchanged.

## Integrated journeys L-Q

### L — default-off isolation

All M4 surfaces are absent by default. Existing discovery remains usable and no canonical capability state changes.

### M — reproducible experiment

One reviewed M3 proposal and immutable base revision produce two competing M4.1 experiments. Repeating the same request preserves experiment/spec/handoff identity. A deterministic external CI fixture consumes only the bounded M4.6 dispatch contract; signed status/result evidence is bound to the exact experiment, spec, handoff, registration and dispatch digests.

### N — lineage and comparison

The two challengers remain separate descendants. Rejected history stays visible, cycles fail closed, and exact lineage replay is deterministic. Fitness evidence keeps quality, regression, cost and runtime dimensions separate. A protected regression failure cannot be compensated for by better quality, cost or runtime evidence.

### O — improver meta-evidence

Unknown provenance remains explicitly `missing` or `redacted`; it is never guessed. Strategy configuration is reduced to a deterministic digest rather than retained as raw configuration. Meta-evaluation compares only compatible versioned development/held-out scopes. A strategy that wins development but regresses held-out does not gain a superiority claim or global score.

### P — curriculum/evaluator protection

Repeated feedback may create a bounded capability-gap record, but feedback text remains untrusted data. Protected evaluator versions are immutable; changing the reviewed suite creates a new version instead of rewriting history. Held-out proposal content is structurally excluded from candidate-generation handoffs.

### Q — runner/security boundary

Tampered or mismatched signed runner evidence fails closed, exact replay is idempotent, stale experiment results fail closed, and cross-organisation reads are rejected. The fixture uses the deterministic in-repository CI adapter; Skillet never shells out, starts arbitrary workloads, invokes a model runtime or receives runner private credentials.

## Promotion semantics

M4 records evidence; it does not own canonical promotion. A `promoted` or `rejected` lineage decision is an immutable review record and does not itself activate a revision, rewrite source, alter governance, or change retrieval ranking. In the integrated fixture, normal source admission creates the challenger revisions before M4 comparisons; all subsequent M4 operations must leave the resulting canonical state and semantic discovery unchanged.

Protected evaluator inputs are similarly immutable. M4.3 policy criteria and M4.4 meta-eval scopes are version-bound evidence, while M4.5 may only propose/review a new suite version. Existing experiment and fitness evidence remains bound to the version under which it was produced.

## External-runner trust boundary

M4.6 is a protocol for data-only dispatch and signed evidence intake, not a remote execution service. Skillet stores public verification keys and bounded runner registration/scope metadata. Private keys, workload credentials, shell access, repository mutation authority, model/GPU execution and scheduling remain outside Skillet.

The acceptance gate verifies deterministic dispatch identity, Ed25519 signature validation, monotonic status evidence, exact-result replay, experiment/spec/handoff binding, explicit resource usage and Skillet-derived budget assessment. Runner-provided payloads cannot redefine protected thresholds or promotion policy.

## Release evidence

`m4-acceptance.json` fails closed unless all six journeys pass and the integrated fixture supplies the required security assertions and immutable evidence identifiers. It preserves parent-gate evidence for:

- M1 integrated acceptance;
- protected M1 retrieval/capability/knowledge metrics;
- M2 enterprise acceptance;
- the complete M3 acceptance report.

The M4 report also retains experiment/spec/handoff, runner dispatch/result, strategy-config and meta-eval-definition digests plus bounded decisions/counts. No M1-M3 threshold is weakened or replaced by M4.
