# Implementation handoff

Skillet is a single Go modular monolith with a runnable v1 compatibility surface and a verified vNext M1 slice. The service retains the v1 repository-admission, durable-package, skill-search/materialisation, lock-restoration, authentication, audit, lifecycle/feedback, and retrieval-evaluation contracts while adding separate capability and organisational-knowledge domains.

The M1 capability surface supports scoped discovery across central and repository-local skills/playbooks plus metadata-only MCP tools. Capability search and knowledge search use separate domain models and indexes. Selected skills can be progressively described and materialised with immutable revision/commit/tree/package provenance. MCP tool metadata is discoverable but is not an execution authority.

The M1 knowledge surface indexes Markdown and OKF content, preserves source/provenance/freshness metadata, supports bounded search/read plus explicit backlinks, and keeps malformed reconciliation from replacing the last authoritative snapshot. Knowledge content does not participate in capability ranking.

Capability governance supports active, deprecated and yanked states, owner/maintainer metadata and explicit replacement guidance. Deprecated capabilities are not silently substituted; yanked capabilities are excluded from normal discovery while retained immutable revisions remain exactly restorable from locks. Revision-bound lifecycle/feedback evidence can deterministically derive reviewable improvement candidates, but those candidates do not mutate source, catalogue state, ranking, governance or active revisions.

The service deliberately remains bounded: it does not execute skill scripts or discovered tools, resolve skill dependencies, orchestrate workflows, generate RAG answers, infer authoritative graph relationships, harvest arbitrary SaaS sources, or automatically edit source/raise issues from evidence. The optional harness-neutral `skillet-client` performs explicit digest-verified acquisition when invoked by a compatible host.

## vNext M1 verification

The accepted direction is tracked by [roadmap issue #30](https://github.com/mhingston/skillet/issues/30), with [ADR-013](adr/ADR-013-vnext-domain-and-module-boundaries.md) as the architecture contract.

Run the authoritative deterministic release gate from the repository root:

```sh
go run ./cmd/skillet-verify
```

The gate runs unit/integration/race checks, the integrated M1 Journey A-E acceptance test, focused compatibility E2Es, and the protected skill/capability/knowledge retrieval evaluations. It emits machine-readable evidence under `artifacts/verification/`, including Journey A-E status, v1 regression comparison, scoped capability metrics, per-kind recall/kind confusion, scope leakage, knowledge metrics, and `go test`/`go vet` status. See [testing-vnext.md](testing-vnext.md) and [m1-release-gate.md](m1-release-gate.md).

## vNext M2 enterprise controls

The M2 direction is tracked by [roadmap issue #51](https://github.com/mhingston/skillet/issues/51) and [ADR-014](adr/ADR-014-enterprise-identity-authorization-boundary.md).

M2 is additive and opt-in. Existing OIDC/JWKS validation remains the authentication boundary; a trusted identity then flows into a separate transport-neutral authorization policy. Provider-specific behaviour should be expressed through configured verified-claim mapping rather than Entra-specific application code. Enterprise authorization must preserve the existing organisation / namespace / repository scope model and remain independent of semantic retrieval ranking.

The implementation is deliberately decomposed into #52-#58: architecture/threat model, policy boundary, claim normalization, scoped enforcement, Entra-compatible user/workload proof, optional audit export, and the integrated enterprise acceptance gate. Development/static/local deployments must not acquire provider-specific dependencies merely because M2 exists.

SCIM, Microsoft Graph, provider-specific SIEM integrations and persistent user/group-directory semantics remain deferred unless a separately reviewed evidence-backed requirement demonstrates that trusted token claims and the generic audit-sink boundary are insufficient.

## Deferred scope after M1

Future work should remain evidence-driven and separately reviewed. Not proven by the M1 gate are dynamic tool execution/brokering, automatic capability composition, public marketplace/submission workflows, generated answers, inferred semantic graph authority, source-specific enterprise harvesters, automatic source mutation, distributed storage/search, or per-user enterprise identity policy.

The demonstration repository `mhingston/agent-skills` remains an external test corpus only; it must never be copied into this repository.
