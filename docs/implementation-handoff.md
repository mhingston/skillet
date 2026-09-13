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

## Deferred scope after M1

Future work should remain evidence-driven and separately reviewed. Not proven by the M1 gate are dynamic tool execution/brokering, automatic capability composition, public marketplace/submission workflows, generated answers, inferred semantic graph authority, source-specific enterprise harvesters, automatic source mutation, distributed storage/search, or per-user enterprise identity policy.

The demonstration repository `mhingston/agent-skills` remains an external test corpus only; it must never be copied into this repository.
