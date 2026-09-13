# vNext verification and regression gates

Skillet vNext treats correctness evidence as part of the change, not as a follow-up activity. The deterministic verification gate is the minimum proof required for code changes that can affect catalogue admission, routing, knowledge retrieval, materialisation, restoration, provenance, or client-visible MCP behaviour.

## One deterministic gate

Run from the repository root:

```sh
go run ./cmd/skillet-verify
```

The command runs, in order:

1. `go test ./...` for unit, property-style, integration, and offline local dependency tests.
2. `go vet ./...` for static correctness checks.
3. `go test -race ./...` for concurrency regressions.
4. the explicit offline skill admission/search/materialisation E2E journey in `internal/e2e`.
5. the explicit offline knowledge index/search/read/reindex E2E journey in `internal/e2e`.
6. the versioned skill-routing retrieval evaluation with its checked-in protected baseline.
7. the versioned knowledge retrieval evaluation with its checked-in protected baseline.

The gate writes machine-readable evidence to `artifacts/verification/`:

- `retrieval.json` is the full skill-routing retrieval evaluator report;
- `knowledge-retrieval.json` is the full knowledge retrieval evaluator report;
- `verification.json` records each gate command plus one row per protected metric with the fixture name/version, observed value, required threshold, comparison direction, baseline value, allowed regression, and pass/fail status.

The gate is intentionally offline and deterministic. It does not require a model endpoint, embedding provider, external Git repository, or other network service.

## Verification layers

The repository keeps several layers because they catch different failures:

- **Unit/property tests** prove small invariants and bounded-input behaviour close to the implementation.
- **Integration tests** use real local SQLite/package stores and the real catalogue, search, restore, knowledge, and HTTP/MCP components.
- **Offline skill E2E** admits a deterministic local skill repository, rebuilds routing documents, proves lexical fallback when embeddings are unavailable, starts a real local Skillet HTTP/MCP server, performs MCP search and explicit candidate selection, materialises through the harness-neutral client, verifies the package digest, and checks immutable revision provenance. The existing remote MCP journey additionally proves locked restoration through the public MCP tool.
- **Offline knowledge E2E** starts from an absent data directory, indexes real Markdown into SQLite/Bleve, searches and reads through the local boundary, modifies/adds/deletes source content, reindexes, and proves stale content disappears while stable identities and updated provenance survive the new snapshot.
- **Retrieval evals** use versioned fixtures and deterministic synthetic vectors to protect both skill-routing and knowledge-retrieval quality.
- **Regression baselines** prevent protected metrics from silently moving backwards even while they remain above a broad acceptance threshold.
- **Negative/security tests** prove fail-closed behaviour such as digest mismatch, provenance mismatch, authentication boundaries, package safety, transactional knowledge publication, stale-document removal, and bounded telemetry/feedback inputs.

The opt-in external corpus test remains useful as additional integration evidence, but it is not part of the deterministic CI gate because it requires network access:

```sh
SKILLET_EXTERNAL_CORPUS=1 go test ./internal/e2e -run TestExternalAgentSkillsCorpus
```

## Protected skill-routing metrics

`evals/retrieval.yaml` remains the source of skill-routing acceptance thresholds. `evals/baselines/retrieval-v1.json` is its deterministic regression baseline. The protected metrics are:

- single-skill top-1 accuracy;
- single-skill recall@3;
- multi-skill recall@5;
- negative-query false activation rate.

## Protected knowledge-retrieval metrics

`evals/knowledge.yaml` is the source of knowledge acceptance thresholds. `evals/baselines/knowledge-v1.json` is its deterministic regression baseline. The protected metrics are:

- knowledge top-1 accuracy;
- knowledge recall@3;
- mean reciprocal rank (MRR);
- negative-query precision.

The knowledge corpus deliberately includes exact matches, semantic paraphrases, heading-sensitive queries, overlapping terminology, an acronym collision, unrelated negatives, and a document that is indexed and then deleted before evaluation. This makes the protected metrics evidence about the Markdown retrieval slice rather than a copy of the skill-routing benchmark.

For both suites, the `max_regression` fixture value defines the permitted movement from the checked-in baseline. CI supplies both baselines on every run; baseline comparison is therefore mandatory rather than an optional local mode.

### Updating a baseline

A baseline change is a deliberate review event, not routine snapshot churn. Update it only when the relevant retrieval fixture, intended ranking semantics, or an accepted algorithmic trade-off changes. In the PR:

1. run the old baseline and show the failing/protected metric;
2. explain why the new result is intended rather than a regression;
3. update the fixture/version when the evaluation population or semantics changed;
4. update the baseline to the reviewed deterministic result;
5. run `go run ./cmd/skillet-verify` and attach or quote the affected metric rows from `verification.json`.

Never weaken both a threshold and its baseline merely to make CI green without evidence for the changed acceptance boundary.

## Failure-path proof

The deterministic suite must continue to prove these behaviours as the implementation evolves:

- lifecycle and feedback observations with mismatched immutable materialisation provenance are rejected;
- quarantined or non-searchable skills cannot become routing candidates;
- locked restoration resolves the recorded immutable revision and digest rather than following a newer or replacement revision;
- digest mismatch fails closed;
- malformed or unreadable knowledge input cannot replace the previously authoritative snapshot;
- interrupted knowledge publication cannot expose a partial catalogue;
- deleted knowledge disappears after a successful reindex rather than remaining stale in the derived index;
- absence or failure of embeddings is visible as degraded operation while deterministic lexical retrieval remains available.

Prefer focused tests for individual failure modes and keep E2E journeys centred on cross-component wiring and public behaviour.

## PR evidence convention

For roadmap implementation PRs, include a **Verification** section with:

```text
Verification
- Gate: go run ./cmd/skillet-verify
- Result: PASS
- Protected metrics: <copy the affected metric rows or concise observed/required values>
- Additional evidence: <only tests or manual proof specific to this change>
```

If the gate cannot run, say exactly which layer is blocked and why. Do not substitute a narrower command while describing the full gate as passing.
