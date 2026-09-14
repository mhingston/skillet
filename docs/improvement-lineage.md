# Opt-in improvement lineage

M4.2 adds a bounded **improvement lineage archive** on top of M4.1 experiments. It records how one immutable capability revision produced candidate or revision descendants, including competing branches and rejected outcomes, without turning that history into package/version semantics.

The feature is disabled by default. Enable only the lineage MCP surface with:

```sh
SKILLET_IMPROVEMENT_LINEAGE=true go run ./cmd/skillet -config skillet.yaml
```

This flag is independent from `SKILLET_IMPROVEMENT_EXPERIMENTS`. A deployment may retain/query already-recorded experiment-backed lineage without exposing experiment-creation tools in the same process.

## What lineage means

A lineage assertion binds:

- the exact immutable parent/base revision and stable capability;
- a descendant identity of kind `candidate` or `revision`;
- one relationship from the deliberately small vocabulary `derived_from`, `challenger_of`, or `superseded_by`;
- one or more exact M4.1 experiment IDs;
- the experiment base revision, originating candidate, status, spec revision, result digest, and eval-suite ID/version when queried;
- actor, correlation, and creation timestamp provenance.

Revision descendants must already exist in the same organization and stable capability. Candidate descendants must match the originating candidate recorded by every supplied producing experiment. Every supplied experiment must belong to the same organization/capability and exact parent revision.

Lineage assertions are append-only and content-addressed. Repeating the same assertion is idempotent; changing its relationship or experiment evidence creates a different historical assertion rather than mutating the old one.

## Decisions

One lineage assertion may receive one immutable terminal decision:

- `promoted`
- `rejected`

The decision includes a bounded reference plus actor/correlation/timestamp provenance. A later attempt to rewrite a rejected assertion as promoted (or vice versa) fails closed. Rejected and losing descendants therefore remain visible as historical evidence.

A lineage decision is **not** activation. It does not change the active revision, searchable state, trust, governance, dependency graph, compatibility, SemVer, or retrieval ranking. Normal source review and ingestion remain authoritative for canonical capability state.

## DAG and cycle safety

Revision-to-revision assertions form a directed acyclic evidence graph. Before inserting an edge, Skillet checks whether the proposed descendant can already reach the proposed parent through existing revision lineage. Self-edges and cycle-forming writes fail with no insertion.

Candidate descendants are leaves in this model: they are recorded as experiment-produced alternatives but cannot be used as parent revisions.

This graph is explicitly **not**:

- a package dependency graph;
- a workflow/orchestration graph;
- an activation graph;
- a trust or governance hierarchy;
- a semantic ranking of which descendant is “best”.

## MCP surface

When `SKILLET_IMPROVEMENT_LINEAGE=true`:

- `record_revision_lineage` — append one immutable experiment-backed assertion;
- `record_lineage_decision` — append the terminal promotion/rejection decision reference;
- `get_revision_lineage` — return a bounded direct-parent/direct-descendant view for one exact revision, hydrated with experiment/eval evidence and decision state.

The read view is scoped through the normal capability authorization resource and organization-bound catalogue lookups. Cross-organization revision IDs or experiment IDs fail closed.

## Audit

Accepted assertions and decisions emit local audit events with bounded capability/revision/lineage/descendant/relationship/decision identifiers. The lineage domain stores references and digests only; it does not copy source bodies, patches, credentials, model prompts, or runner execution authority.

## Verification

Focused and end-to-end tests cover:

- default-off vs explicit opt-in tool discovery;
- linear `A -> B -> C` lineage;
- competing `A -> B` and `A -> C` descendants;
- rejected challengers remaining queryable;
- candidate descendants bound to exact experiment provenance;
- active/searchable canonical state remaining unchanged;
- organization-scope isolation;
- malformed relationships and cyclic revision writes failing closed;
- immutable terminal decision evidence.

The existing repository-wide M1-M3/M4.1 regression gate remains unchanged:

```sh
go run ./cmd/skillet-verify
```
