# vNext M1 deterministic release gate

This is the operator/developer quickstart for proving the vNext M1 contract without cloud services, live model APIs, or external repositories.

## Run the complete gate

From the repository root with Go 1.25 installed:

```sh
go run ./cmd/skillet-verify
```

That command is the release-level source of truth used by CI. Do not replace it with a narrower test command when claiming M1 verification.

The gate writes evidence to `artifacts/verification/`:

- `verification.json` — overall gate result, every executed command, Journey A-E status, protected threshold rows, capability recall-by-kind and kind-confusion;
- `retrieval.json` — protected v1 skill-routing evaluation and baseline comparison;
- `capability-retrieval.json` — scoped skill/playbook/tool retrieval, negative activation, per-kind recall, kind confusion and scope leakage;
- `knowledge-retrieval.json` — protected Markdown/knowledge retrieval evaluation and baseline comparison.

CI uploads the verification evidence even when the gate fails so the failing layer remains inspectable.

## What the integrated fixture proves

`TestOfflineM1IntegratedAcceptance` creates one deterministic local organisation and composes the real catalogue, package store, retrieval indexes, knowledge store and Streamable HTTP MCP server. Its fixture includes central skills, two repository-local skill sources with overlapping release language, metadata-only MCP tool catalogues, a playbook descriptor, Markdown plus OKF knowledge, deprecated/replacement and yanked capabilities, revision-bound evidence, a malformed skill and a malformed OKF update.

The five release journeys are:

1. **Journey A — capability discovery/materialisation.** Repository scope exposes local + central candidates without cross-repository leakage; skill/playbook/tool kinds remain distinct; skill detail is progressively disclosed; explicit materialisation preserves revision, commit, tree and package digest; the v1 search surface remains available.
2. **Journey B — organisational knowledge.** Markdown/OKF content is searchable/readable with source revision, OKF provenance/freshness metadata and explicit backlinks. A knowledge-only marker is asserted not to enter capability ranking.
3. **Journey C — governance/reproducibility.** Deprecated replacement guidance is visible without silent substitution; yanked capabilities are excluded from normal discovery; the exact retained yanked revision is restorable with its recorded digest/commit/tree.
4. **Journey D — learning loop.** Lifecycle/feedback evidence is bound to an exact materialisation provenance; improvement candidates remain evidence-traceable, deterministic review artefacts and do not change the active revision.
5. **Journey E — degraded/failure modes.** With no embedding backend, capability retrieval reports deterministic lexical degradation; forged scope is rejected; malformed skill input is quarantined; malformed OKF reconciliation fails without corrupting the last-good authoritative knowledge snapshot; discovered MCP tools remain metadata-only.

Focused unit and E2E tests remain alongside this integrated test for narrower diagnostics and additional edge cases such as MCP catalogue last-good refresh behaviour, detailed governance transitions, stale-link reconciliation and evidence deduplication.

## Release acceptance

A candidate M1 revision is releasable only when:

- `verification.json.passed` is `true`;
- every `e2e_journeys[*].passed` value is `true`;
- protected v1 routing metrics satisfy both their accepted thresholds and baseline-regression allowances;
- scoped capability top-1/recall@k/multi-recall/per-kind recall thresholds pass;
- capability negative false activation passes and deterministic fixture scope leakage remains zero;
- protected knowledge metrics meet their accepted thresholds and baseline-regression allowances;
- `go test ./...`, `go vet ./...`, `go test -race ./...` and the explicit integrated M1 E2E step all pass.

Thresholds remain owned by their versioned evaluator fixtures. The M1 gate only aggregates and reports them; it must not lower an owning threshold merely to make CI green.

## Supported boundary

M1 proves discovery, progressive disclosure, reproducible skill materialisation, knowledge retrieval, lightweight catalogue governance and review-only evidence derivation. It does **not** prove dynamic tool execution, workflow orchestration, generated RAG answers, automatic source mutation, marketplace workflows, arbitrary SaaS harvesting, inferred authoritative graph relationships or horizontally distributed operation.
