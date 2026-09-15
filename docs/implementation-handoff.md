# Implementation handoff

Skillet is a single Go modular monolith with a runnable v1 compatibility surface and verified vNext M1, M2, M3, and M4 surfaces. The service retains the v1 repository-admission, durable-package, skill-search/materialisation, lock-restoration, authentication, audit, lifecycle/feedback, and retrieval-evaluation contracts while adding separate capability and organisational-knowledge domains, opt-in enterprise authorization/audit export, bounded human/adoption adapters, and opt-in capability-evolution evidence/control-plane primitives.

The M1 capability surface supports scoped discovery across central and repository-local skills/playbooks plus metadata-only MCP tools. Capability search and knowledge search use separate domain models and indexes. Selected skills can be progressively described and materialised with immutable revision/commit/tree/package provenance. MCP tool metadata is discoverable but is not an execution authority.

The M1 knowledge surface indexes Markdown and OKF content, preserves source/provenance/freshness metadata, supports bounded search/read plus explicit backlinks, and keeps malformed reconciliation from replacing the last authoritative snapshot. Knowledge content does not participate in capability ranking.

Capability governance supports active, deprecated and yanked states, owner/maintainer metadata and explicit replacement guidance. Deprecated capabilities are not silently substituted; yanked capabilities are excluded from normal discovery while retained immutable revisions remain exactly restorable from locks. Revision-bound lifecycle/feedback evidence can deterministically derive reviewable improvement candidates without changing ranking or governance.

M2 adds provider-neutral trusted identity/authorization and optional audit export. Entra-compatible delegated-user and workload tokens are acceptance-tested against the same generic policy model; provider-specific directory semantics remain outside the core.

M3 keeps the browser UI as an adapter over those same boundaries. It adds server-rendered catalogue/knowledge/operator views, deterministic declared capability composition and collections, a read-only Claude Code marketplace distribution profile, bounded discussions/watch/moderation/activity state, and provenance-bound reviewable improvement proposals. Collaboration signals do not affect semantic ranking; dependency resolution does not execute capabilities or invent undeclared edges; distribution does not become a second publishing authority; proposals do not automatically edit source or activate revisions.

M4 adds explicitly enabled improvement experiments, append-only revision lineage, scoped champion/challenger fitness evidence, improver provenance and held-out meta-evaluation, bounded curriculum/gap evidence, and a signed external-runner evidence protocol. These are evidence/control-plane primitives only: M4 does not execute arbitrary workloads, train models, rewrite source, alter semantic ranking/governance, mutate protected evaluators in place, or automatically promote revisions. External runners execute outside Skillet and return signed, digest-bound status/result evidence.

The service remains deliberately bounded: it does not execute skill scripts or discovered tools, orchestrate workflows, generate RAG answers, infer authoritative graph relationships, harvest arbitrary SaaS sources, operate a public submission marketplace, automatically merge/apply proposed source changes, or become a general-purpose agent/model runtime. The optional harness-neutral `skillet-client` performs explicit digest-verified acquisition when invoked by a compatible host.

## Verified release gate

The M1 product direction is recorded by [roadmap issue #30](https://github.com/mhingston/skillet/issues/30), with [ADR-013](adr/ADR-013-vnext-domain-and-module-boundaries.md) as the architecture contract. The completed M2 enterprise-controls milestone is recorded by [roadmap issue #51](https://github.com/mhingston/skillet/issues/51), with [ADR-014](adr/ADR-014-enterprise-identity-authorization-boundary.md) as its security and authorization contract. M3 is bounded by [ADR-015](adr/ADR-015-m3-human-ui-collaboration-composition-distribution-boundaries.md) and release-gated by issue #75. M4 capability evolution is tracked by [roadmap issue #82](https://github.com/mhingston/skillet/issues/82) and integrated by issue #89.

Run the authoritative deterministic release gate from the repository root:

```sh
go run ./cmd/skillet-verify
```

The gate preserves unit/integration/race checks, the integrated M1 Journey A-E acceptance test, focused compatibility E2Es, and the protected skill/capability/knowledge retrieval evaluations. It also runs the M2 enterprise acceptance journey, M3 Journeys F-K, and M4 Journeys L-Q. M4's integrated journey covers default-off isolation, deterministic experiment/runner evidence, competing lineage and protected fitness comparison, scoped improver meta-evaluation, curriculum/held-out protection, and stale/replay/tamper/cross-scope security cases without weakening earlier thresholds.

M3 includes a Go-controlled real-process browser smoke: the test builds and starts the actual `skillet` binary and drives its server-rendered UI with headless Chrome/Chromium. The richer scoped and stateful browser journeys remain deterministic Go E2Es over the same application adapters so failures stay local and diagnosable.

Machine-readable evidence is emitted under `artifacts/verification/`, including `verification.json`, protected retrieval reports, `enterprise-acceptance.json`, `m3-acceptance.json`, `m4-integrated.json`, and normalized `m4-acceptance.json`. The M4 report preserves the parent M1-M3 status and records immutable handoff/spec/runner/provenance/meta-eval evidence. CI retains the full directory as a workflow artifact. See [testing-vnext.md](testing-vnext.md), [m1-release-gate.md](m1-release-gate.md), [enterprise-operations.md](enterprise-operations.md), [m3-release-gate.md](m3-release-gate.md), and [m4-release-gate.md](m4-release-gate.md).

## Shipped M2 enterprise controls

M2 is additive and opt-in. Existing OIDC/JWKS validation remains the authentication boundary; a trusted identity then flows into a separate transport-neutral authorization policy. Provider-specific behaviour is expressed through configured verified-claim mapping rather than Entra-specific application code. Enterprise authorization preserves the existing organisation / namespace / repository scope model and remains independent of semantic retrieval ranking.

The completed #52-#58 slices provide the architecture/threat model, policy boundary, claim normalization, scoped enforcement, Entra-compatible delegated-user and workload proof, optional audit export, and the integrated enterprise acceptance gate. Development/static/local deployments do not acquire provider-specific dependencies merely because M2 exists.

SCIM, Microsoft Graph, provider-specific SIEM integrations and persistent user/group-directory semantics remain deferred unless a separately reviewed evidence-backed requirement demonstrates that trusted token claims and the generic audit-sink boundary are insufficient.

## Shipped M3 human/adoption surface

The M3 implementation keeps source-of-truth ownership explicit. Git/supported ingestion remains authoritative for capabilities, Markdown/OKF remains authoritative for knowledge, existing governance remains authoritative for lifecycle, and trusted M2 identity/policy remains authoritative for authorization. M3-owned discussion/watch/proposal state is bounded auxiliary state linked to stable capability/revision identity.

Declared composition is deterministic and provenance-locked. Required dependencies are resolved transitively with explicit version constraints, cycles/conflicts/unavailable dependencies fail closed, collections expand deterministically, and unauthorised dependency identity is not leaked. Recommended capabilities remain guidance rather than implicit activation.

The Claude Code distribution profile is a deterministic authorized projection of immutable Git provenance. Yanked, inaccessible, or invalid sources cannot leak into the manifest. Distribution does not publish or mutate source.

Reviewable improvement proposals bind evidence and candidate identity to an immutable base revision, redact bounded source context, retain verification attempts, fail closed on stale bases, and never update the canonical source or active revision automatically.

## Shipped M4 capability-evolution evidence

M4 is additive and default-off. Experiments, lineage, fitness, improver meta-evaluation, curriculum evidence and external runners each require their explicit feature flag; runner dispatch additionally depends on experiments. Feature enablement only exposes eligible MCP tools—the normal authentication/authorization path remains authoritative.

Experiments are immutable, proposal/base-bound specifications with deterministic handoff digests and explicit protected-eval/budget contracts. Lineage is append-only and retains losing/rejected branches. Fitness evidence stays scoped to immutable eval-suite/task-distribution versions and treats protected regression criteria as gates rather than a score that can be traded away against quality, cost or runtime.

Improver provenance represents unknown or withheld fields explicitly as `missing` or `redacted`; strategy configuration is retained as a deterministic digest. Meta-evaluation compares strategies only on compatible development/held-out scopes and refuses to turn an overfit development win into a universal superiority claim.

Curriculum/gap records treat feedback as untrusted evidence, can propose reviewed development/protected-eval artefacts, and version protected evaluator updates rather than mutating active suites in place. Held-out cases are structurally excluded from candidate-generation handoffs.

External runners are registered public-key identities with bounded capabilities/scope. Skillet issues data-only immutable dispatches and verifies signed monotonic status/result evidence. The runner keeps its private key, workload credentials, scheduling, repository mutation authority and model/GPU execution outside Skillet. Exact replay is idempotent; tampered, mismatched, stale and cross-scope evidence fails closed.

See [m4-release-gate.md](m4-release-gate.md) for the full enablement, security/trust, promotion and machine-evidence contract.

## Deferred scope after M4

Future work should remain evidence-driven and separately reviewed. Not proven by the current release gate are dynamic tool execution/brokering inside Skillet, workflow orchestration, automatic proposal application/merge, public marketplace/submission workflows, generated answers, inferred semantic graph authority, source-specific enterprise harvesters, distributed storage/search, or autonomous model training. The M4 external-runner protocol is deliberately not proof of any of those capabilities.

The demonstration repository `mhingston/agent-skills` remains an external test corpus only; it must never be copied into this repository.
