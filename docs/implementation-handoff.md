# Implementation handoff

Skillet is a single Go modular monolith with a runnable v1 compatibility surface and verified vNext M1, M2, and M3 surfaces. The service retains the v1 repository-admission, durable-package, skill-search/materialisation, lock-restoration, authentication, audit, lifecycle/feedback, and retrieval-evaluation contracts while adding separate capability and organisational-knowledge domains, opt-in enterprise authorization/audit export, and bounded human/adoption adapters.

The M1 capability surface supports scoped discovery across central and repository-local skills/playbooks plus metadata-only MCP tools. Capability search and knowledge search use separate domain models and indexes. Selected skills can be progressively described and materialised with immutable revision/commit/tree/package provenance. MCP tool metadata is discoverable but is not an execution authority.

The M1 knowledge surface indexes Markdown and OKF content, preserves source/provenance/freshness metadata, supports bounded search/read plus explicit backlinks, and keeps malformed reconciliation from replacing the last authoritative snapshot. Knowledge content does not participate in capability ranking.

Capability governance supports active, deprecated and yanked states, owner/maintainer metadata and explicit replacement guidance. Deprecated capabilities are not silently substituted; yanked capabilities are excluded from normal discovery while retained immutable revisions remain exactly restorable from locks. Revision-bound lifecycle/feedback evidence can deterministically derive reviewable improvement candidates without changing ranking or governance.

M2 adds provider-neutral trusted identity/authorization and optional audit export. Entra-compatible delegated-user and workload tokens are acceptance-tested against the same generic policy model; provider-specific directory semantics remain outside the core.

M3 keeps the browser UI as an adapter over those same boundaries. It adds server-rendered catalogue/knowledge/operator views, deterministic declared capability composition and collections, a read-only Claude Code marketplace distribution profile, bounded discussions/watch/moderation/activity state, and provenance-bound reviewable improvement proposals. Collaboration signals do not affect semantic ranking; dependency resolution does not execute capabilities or invent undeclared edges; distribution does not become a second publishing authority; proposals do not automatically edit source or activate revisions.

The service remains deliberately bounded: it does not execute skill scripts or discovered tools, orchestrate workflows, generate RAG answers, infer authoritative graph relationships, harvest arbitrary SaaS sources, operate a public submission marketplace, or automatically merge/apply proposed source changes. The optional harness-neutral `skillet-client` performs explicit digest-verified acquisition when invoked by a compatible host.

## Verified release gate

The M1 product direction is recorded by [roadmap issue #30](https://github.com/mhingston/skillet/issues/30), with [ADR-013](adr/ADR-013-vnext-domain-and-module-boundaries.md) as the architecture contract. The completed M2 enterprise-controls milestone is recorded by [roadmap issue #51](https://github.com/mhingston/skillet/issues/51), with [ADR-014](adr/ADR-014-enterprise-identity-authorization-boundary.md) as its security and authorization contract. M3 is bounded by [ADR-015](adr/ADR-015-m3-human-ui-collaboration-composition-distribution-boundaries.md) and release-gated by issue #75.

Run the authoritative deterministic release gate from the repository root:

```sh
go run ./cmd/skillet-verify
```

The gate preserves unit/integration/race checks, the integrated M1 Journey A-E acceptance test, focused compatibility E2Es, and the protected skill/capability/knowledge retrieval evaluations. It also runs the M2 enterprise acceptance journey and M3 Journeys F-K covering the human catalogue, deterministic composition/locks, host-native distribution, collaboration/learning signals, reviewable improvement proposals, and operator/admin security.

M3 includes a Go-controlled real-process browser smoke: the test builds and starts the actual `skillet` binary and drives its server-rendered UI with headless Chrome/Chromium. The richer scoped and stateful browser journeys remain deterministic Go E2Es over the same application adapters so failures stay local and diagnosable.

Machine-readable evidence is emitted under `artifacts/verification/`, including `verification.json`, protected retrieval reports, `enterprise-acceptance.json`, and `m3-acceptance.json`. CI prints the aggregate/M2/M3 evidence and retains the full directory as a workflow artifact. See [testing-vnext.md](testing-vnext.md), [m1-release-gate.md](m1-release-gate.md), [enterprise-operations.md](enterprise-operations.md), and [m3-release-gate.md](m3-release-gate.md).

## Shipped M2 enterprise controls

M2 is additive and opt-in. Existing OIDC/JWKS validation remains the authentication boundary; a trusted identity then flows into a separate transport-neutral authorization policy. Provider-specific behaviour is expressed through configured verified-claim mapping rather than Entra-specific application code. Enterprise authorization preserves the existing organisation / namespace / repository scope model and remains independent of semantic retrieval ranking.

The completed #52-#58 slices provide the architecture/threat model, policy boundary, claim normalization, scoped enforcement, Entra-compatible delegated-user and workload proof, optional audit export, and the integrated enterprise acceptance gate. Development/static/local deployments do not acquire provider-specific dependencies merely because M2 exists.

SCIM, Microsoft Graph, provider-specific SIEM integrations and persistent user/group-directory semantics remain deferred unless a separately reviewed evidence-backed requirement demonstrates that trusted token claims and the generic audit-sink boundary are insufficient.

## Shipped M3 human/adoption surface

The M3 implementation keeps source-of-truth ownership explicit. Git/supported ingestion remains authoritative for capabilities, Markdown/OKF remains authoritative for knowledge, existing governance remains authoritative for lifecycle, and trusted M2 identity/policy remains authoritative for authorization. M3-owned discussion/watch/proposal state is bounded auxiliary state linked to stable capability/revision identity.

Declared composition is deterministic and provenance-locked. Required dependencies are resolved transitively with explicit version constraints, cycles/conflicts/unavailable dependencies fail closed, collections expand deterministically, and unauthorised dependency identity is not leaked. Recommended capabilities remain guidance rather than implicit activation.

The Claude Code distribution profile is a deterministic authorized projection of immutable Git provenance. Yanked, inaccessible, or invalid sources cannot leak into the manifest. Distribution does not publish or mutate source.

Reviewable improvement proposals bind evidence and candidate identity to an immutable base revision, redact bounded source context, retain verification attempts, fail closed on stale bases, and never update the canonical source or active revision automatically.

## Deferred scope after M3

Future work should remain evidence-driven and separately reviewed. Not proven by the current release gate are dynamic tool execution/brokering, workflow orchestration, automatic proposal application/merge, public marketplace/submission workflows, generated answers, inferred semantic graph authority, source-specific enterprise harvesters, or distributed storage/search.

The demonstration repository `mhingston/agent-skills` remains an external test corpus only; it must never be copied into this repository.
