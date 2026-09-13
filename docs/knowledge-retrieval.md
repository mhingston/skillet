# Markdown knowledge retrieval

Skillet vNext has a knowledge retrieval domain that is deliberately separate from capability/skill discovery. The implementation lives under `internal/knowledge` and shares only domain-neutral retrieval mechanics (`internal/retrieval`): lexical search, cosine ranking, reciprocal-rank fusion, and the generic reranker contract.

This first vertical slice indexes Markdown from local source snapshots. A source root can be a normal directory or an already checked-out Git worktree. Git fetching/synchronisation is an adapter concern: callers provide the stable source ID, source locator, and observed revision (for example a commit SHA) when publishing a snapshot.

## Identity and provenance

Document identity is derived from the stable source ID plus the normalised relative path. Editing a file therefore preserves its document ID. Each ATX-heading section becomes a chunk; its chunk ID is derived from the document ID, heading path, and section content digest. An unchanged section retains its chunk ID even when another section in the same document changes.

Every search result includes:

- stable document and chunk IDs;
- source ID, locator, and revision;
- relative source path and document digest;
- heading and heading path;
- bounded snippet and section digest;
- lexical/vector ranks plus the RRF score;
- optional reranker reason.

Full chunk content is returned only by the explicit read operation after a result has been selected.

## Local boundary

The local CLI is intentionally small; MCP/API exposure is handled by the later knowledge API roadmap issue.

```sh
# Index a local directory or Git worktree snapshot.
go run ./cmd/skillet-knowledge index \
  -data-dir .skillet/knowledge \
  -source-root ./handbook \
  -source-id engineering-handbook \
  -locator https://github.com/example/handbook \
  -revision <commit-sha>

# Retrieve bounded snippets and provenance.
go run ./cmd/skillet-knowledge search \
  -data-dir .skillet/knowledge \
  -query "how do we rollback a production outage?" \
  -limit 5

# Read the complete selected section.
go run ./cmd/skillet-knowledge read \
  -data-dir .skillet/knowledge \
  -chunk-id <chunk-id>
```

The CLI currently uses lexical retrieval because it does not accept provider credentials. Embedding-enabled callers construct `knowledge.Service` with any implementation of the existing `Embed(string) ([]float32, error)` contract; `internal/embedding.Client` satisfies that interface. If embedding calls fail, the snapshot remains searchable lexically and the result diagnostics explicitly report vector degradation.

## Reindex semantics

A reindex is snapshot replacement, not incremental mutation of the authoritative catalogue:

1. discover and parse every Markdown file;
2. compute stable identities and section revisions;
3. obtain embeddings when available, degrading to lexical-only for provider failures;
4. build the complete next in-memory lexical index;
5. replace the SQLite document/chunk catalogue in one transaction;
6. atomically swap the derived in-memory index.

Malformed or unreadable input and index-construction failures happen before publication. A cancelled or failed SQLite publication leaves the previous catalogue and index authoritative. Deleted documents disappear on the next successful snapshot replacement.

## Deterministic evaluation

`evals/knowledge.yaml` and `evals/knowledge-corpus-v1/` define the versioned offline knowledge suite. It includes exact matches, semantic paraphrases, heading-sensitive queries, overlapping terminology, KPI acronym ambiguity, unrelated negatives, and a stale document that is deliberately deleted between the first and second index pass.

Run it directly with:

```sh
go run ./cmd/skillet-knowledge-eval \
  --fixtures evals/knowledge.yaml \
  --baseline evals/baselines/knowledge-v1.json \
  --report artifacts/verification/knowledge-retrieval.json
```

The repository-wide `go run ./cmd/skillet-verify` gate also runs the knowledge E2E journey and protected knowledge evaluation. It remains fully offline and uses deterministic local embeddings for semantic test cases.
