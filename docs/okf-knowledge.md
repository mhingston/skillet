# OKF knowledge ingestion and MCP

Skillet consumes Open Knowledge Format (OKF) as an **input/interchange contract**. It does not harvest Confluence, Jira, Snowflake, or other source systems into OKF, and it does not use an LLM to generate answers or infer authoritative graph relationships.

This slice implements the subset needed for local organisational-knowledge bundles:

- UTF-8 Markdown concept files with YAML frontmatter;
- required non-empty `type`;
- optional `title`, `description`, `resource`, `tags`;
- provenance via `sources` and `usage_window`;
- generation/verification metadata, including the single-mapping `verified` shorthand;
- lifecycle/freshness via `status`, `stale_after`, and legacy `timestamp` preservation;
- unknown producer frontmatter preserved as data;
- ordinary bundle-relative or relative Markdown links;
- broken links retained as unresolved data rather than rejected or inferred.

`index.md` and `log.md` are reserved OKF files and are not indexed as concept documents. Symbolic links inside a bundle are rejected so a bundle cannot make Skillet read outside its declared root. Concept files are bounded by the same 4 MiB Markdown limit as the existing knowledge slice.

## Identity and reconciliation

A document ID is derived from the stable bundle ID and relative concept path, using the same knowledge identity function introduced in #34. Content edits therefore keep document identity stable; a path rename is a delete plus add, matching the OKF concept-ID definition. Chunk IDs continue to change when section/content revisions change.

`ReindexOKF` validates the complete candidate snapshot and builds the derived retrieval index before publication. Documents, chunks, OKF metadata, and explicit links are then replaced in one SQLite transaction. A malformed bundle update therefore leaves the previous authoritative snapshot readable.

Case-folded concept-path collisions (for example `Policy.md` and `policy.md`) are rejected so identity is deterministic across filesystems.

## MCP surface

`skillet-knowledge-mcp` exposes exactly three knowledge operations over stateless Streamable HTTP MCP:

- `search_knowledge`: bounded to 10 compact results and returns source revision plus preserved OKF metadata/provenance;
- `read_knowledge`: reads one selected chunk and its document-level provenance/outgoing explicit links;
- `get_backlinks`: returns only resolved incoming Markdown links. No semantic relationship inference is performed.

Returned document text and metadata are untrusted data. They are never interpreted as registry/server instructions and no execution broker is introduced.

Example local server:

```sh
go run ./cmd/skillet-knowledge-mcp \
  -data-dir ./data/knowledge \
  -bundle-root ./knowledge-bundle \
  -bundle-id organisation-wiki \
  -revision "$(git -C ./knowledge-bundle rev-parse HEAD)" \
  -listen 127.0.0.1:8081
```

The MCP endpoint is `http://127.0.0.1:8081/mcp` by default.

## Verification

The checked-in fixture under `internal/knowledge/testdata/okf-v02` is synthetic repository-owned test data shaped after the public OKF v0.2 specification. The offline E2E starts a real Streamable HTTP MCP server, searches and reads knowledge, checks provenance and backlinks, performs add/update/delete reconciliation, verifies stable unchanged identity, and proves a malformed follow-up cannot corrupt the last committed state.

The #32 verification gate already runs `go test ./...`, so the OKF E2E and negative parser/security cases are part of the same project gate while the protected #34 retrieval metrics remain unchanged.
