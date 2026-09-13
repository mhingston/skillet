# vNext verification and regression gates

Skillet vNext treats correctness evidence as part of the change, not as a follow-up activity. The deterministic verification gate is the minimum proof required for code changes that can affect catalogue admission, routing, knowledge retrieval, materialisation, restoration, provenance, governance, evidence, or client-visible MCP behaviour.

## One deterministic gate

Run from the repository root:

```sh
go run ./cmd/skillet-verify
```

The command runs, in order:

1. `go test ./...` for unit, property-style, integration, and offline local dependency tests.
2. `go vet ./...` for static correctness checks.
3. `go test -race ./...` for concurrency regressions.
4. the explicit integrated M1 Journey A-E acceptance test in `internal/e2e` against one composed local Skillet server.
5. focused offline skill/scoped-capability/knowledge E2E journeys retained as narrower compatibility diagnostics.
6. the versioned v1 skill-routing retrieval evaluation with its checked-in protected baseline.
7. the versioned scoped capability retrieval evaluation covering skills/playbooks/tools, negative activation and scope isolation.
8. the versioned knowledge retrieval evaluation with its checked-in protected baseline.

The gate writes machine-readable evidence to `artifacts/verification/`:

- `retrieval.json` is the full v1 skill-routing retrieval evaluator report;
- `capability-retrieval.json` is the full scoped capability evaluator report, including per-kind recall and kind confusion;
- `knowledge-retrieval.json` is the full knowledge retrieval evaluator report;
- `verification.json` records every gate command, Journey A-E status, one row per protected metric with the fixture name/version, observed value, required threshold, comparison direction, baseline value/allowed regression where applicable, plus capability recall-by-kind and kind-confusion summaries.

The gate is intentionally offline and deterministic. It does not require a model endpoint, embedding provider, external Git repository, or other network service. See [m1-release-gate.md](m1-release-gate.md) for the release-level quickstart and acceptance boundary.

## Verification layers

The repository keeps several layers because they catch different failures:

- **Unit/property tests** prove small invariants and bounded-input behaviour close to the implementation.
- **Integration tests** use real local SQLite/package stores and the real catalogue, search, restore, knowledge, governance/evidence, and HTTP/MCP components.
- **Integrated M1 E2E** composes scoped central/repository-local capabilities, metadata-only MCP tools, a playbook, Markdown/OKF knowledge, governance, exact restore and revision-bound evidence behind one Skillet MCP server. It proves Journey A-E without cloud or live-model dependencies.
- **Focused skill E2E** admits deterministic local skill repositories, proves quarantine and lexical fallback, performs MCP search/explicit selection/materialisation, verifies package digests and immutable revision provenance, and protects the v1 surface.
- **Focused knowledge E2E** indexes real Markdown/OKF into SQLite/Bleve, searches/reads progressively, follows explicit backlinks, modifies/adds/deletes source content, reindexes, and proves stale content disappears while stable identities/provenance survive valid snapshots and malformed snapshots do not replace authority.
- **Focused governance/evidence E2Es** prove deprecated/yanked lifecycle rules, exact historical restore, evidence provenance, deterministic candidate derivation, contradiction handling and review-only non-effects.
- **Retrieval evals** use versioned fixtures and deterministic synthetic vectors to protect skill-routing, scoped capability and knowledge-retrieval quality.
- **Regression baselines** prevent protected v1/knowledge metrics from silently moving backwards even while they remain above a broad acceptance threshold.
- **Negative/security tests** prove fail-closed behaviour such as digest mismatch, provenance mismatch, forged scope, authentication boundaries, package safety, transactional knowledge publication, stale-document removal, bounded telemetry/feedback and metadata-only MCP tool non-execution.

The opt-in external corpus test remains useful as additional integration evidence, but it is not part of the deterministic CI gate because it requires network access:

```sh
SKILLET_EXTERNAL_CORPUS=1 go test ./internal/e2e -run TestExternalAgentSkillsCorpus
```

## Protected v1 skill-routing metrics

`evals/retrieval.yaml` remains the source of v1 skill-routing acceptance thresholds. `evals/baselines/retrieval-v1.json` is its deterministic regression baseline. The protected metrics are:

- single-skill top-1 accuracy;
- single-skill recall@3;
- multi-skill recall@5;
- negative-query false activation rate.

## Protected scoped-capability metrics

`evals/capabilities.yaml` owns the scoped capability thresholds. The verifier reports:

- capability top-1 accuracy;
- recall@3;
- multi-capability recall@5;
- per-kind recall for skill, playbook and tool where thresholds are defined;
- negative false activation;
- deterministic scope leakage;
- the full expected-kind versus returned-kind confusion matrix.

Scope leakage must remain zero for deterministic isolation fixtures. Per-kind thresholds remain owned by the versioned capability fixture and are not duplicated or weakened by the aggregate release gate.

## Protected knowledge-retrieval metrics

`evals/knowledge.yaml` is the source of knowledge acceptance thresholds. `evals/baselines/knowledge-v1.json` is its deterministic regression baseline. The protected metrics are:

- knowledge top-1 accuracy;
- knowledge recall@3;
- mean reciprocal rank (MRR);
- negative-query precision.

The knowledge corpus deliberately includes exact matches, semantic paraphrases, heading-sensitive queries, overlapping terminology, an acronym collision, unrelated negatives, and a document that is indexed and then deleted before evaluation. This makes the protected metrics evidence about the knowledge retrieval slice rather than a copy of the skill-routing benchmark.

For baseline-backed suites, the `max_regression` fixture value defines the permitted movement from the checked-in baseline. CI supplies the baselines on every run; baseline comparison is therefore mandatory rather than an optional local mode.

### Updating a baseline or threshold

A baseline/threshold change is a deliberate review event, not routine snapshot churn. Update it only when the owning fixture, intended retrieval semantics, or an accepted algorithmic trade-off changes. In the PR:

1. run the previous accepted gate and show the failing/protected metric;
2. explain why the new result/threshold is intended rather than a regression workaround;
3. update the owning fixture/version when the evaluation population or semantics changed;
4. update any baseline to the reviewed deterministic result;
5. run `go run ./cmd/skillet-verify` and attach or quote the affected rows from `verification.json`.

Never weaken an owning threshold or baseline merely to make CI green without separately reviewed evidence for the changed acceptance boundary.

## Failure-path proof

The deterministic suite must continue to prove these behaviours as the implementation evolves:

- lifecycle and feedback observations with mismatched immutable materialisation provenance are rejected;
- quarantined or non-searchable skills cannot become routing candidates;
- forged/traversal scope cannot broaden repository visibility;
- deprecated replacement guidance never silently substitutes the explicitly selected revision;
- yanked capabilities are excluded from normal discovery/new selection but exact retained revisions remain lock-restorable;
- locked restoration resolves the recorded immutable revision and digest rather than following a newer or replacement revision;
- digest mismatch fails closed;
- malformed or unreadable knowledge input cannot replace the previously authoritative snapshot;
- interrupted knowledge publication cannot expose a partial catalogue;
- deleted knowledge disappears after a successful reindex rather than remaining stale in the derived index;
- absence or failure of embeddings is visible as degraded operation while deterministic lexical retrieval remains available;
- metadata-only MCP tool discovery never creates an execution/proxy/credential authority;
- improvement-candidate reads do not mutate source, active revision, ranking or governance.

Prefer focused tests for individual failure modes and keep the integrated M1 E2E centred on cross-component wiring and public behaviour.

## PR evidence convention

For roadmap implementation PRs, include a **Verification** section with:

```text
Verification
- Gate: go run ./cmd/skillet-verify
- Result: PASS
- Journey A-E: PASS
- Protected metrics: <copy the affected metric rows or concise observed/required values>
- Additional evidence: <only tests or manual proof specific to this change>
```

If the gate cannot run, say exactly which layer is blocked and why. Do not substitute a narrower command while describing the full gate as passing.
