# ADR-012: Cross-ID successor lineage is publisher-declared catalogue state

## Status

Accepted. The original successor-lineage contract is retained, and #38 extends the surrounding catalogue governance model with explicit `active`, `deprecated`, and `yanked` lifecycle state. The implementation remains a small presentation/discovery slice and does not introduce automatic upgrade or workflow semantics.

## Context

Skillet already preserves immutable revisions and can resolve SemVer within one stable skill identity. That solves version drift when a maintained skill evolves under the same `skill_id`.

It does not solve a different form of drift: a maintainer may replace one skill with a differently named or differently located skill. In that case SemVer cannot tell a consumer that new work should normally use the successor, even though the older skill must remain reproducible for lock restoration and historical runs.

Semantic neighbours are also insufficient evidence for replacement. Similarity can indicate overlap or adjacency, but cannot establish maintainer intent.

## Decision

Skillet supports one explicit cross-ID successor relationship:

```yaml
metadata:
  skillet.deprecated: "true"
  skillet.replaced_by: "org/repository/new-skill"
```

The namespaced metadata keys are publisher declarations, not inferred relationships.

This slice intentionally does not add `supersedes`, `alternative_to`, dependency, composition, or workflow-order semantics.

### Stable-identity semantics

Deprecation and replacement describe the **current catalogue state of the stable skill identity**, not the state of every historical package revision.

The declaration is read from the active source revision and projected onto the stable `skill_id` for discovery/presentation. Historical revisions remain immutable and retain their original package contents, commit/tree identity, digests, and restoration behaviour.

If a later active source revision removes the declaration, the current stable identity is no longer presented as replaced. Skillet does not rewrite historical revisions to reflect that change.

### Target identity

`skillet.replaced_by` names a stable Skillet capability/skill ID, not a version or revision ID. The relationship is organisation-scoped and may cross repositories within the same organisation when the target remains visible everywhere the deprecated source is visible.

The source must not point to itself. A replacement target need not be present at the exact instant the source is admitted because repositories may synchronize independently. A missing target is therefore an unresolved catalogue reference to surface to maintainers/consumers, not a reason to mutate or silently redirect the source.

### Deprecation and the legacy shorthand

The original shorthand remains deliberately strict for backwards compatibility:

- `skillet.deprecated: "true"` requires a non-empty `skillet.replaced_by`;
- `skillet.replaced_by` requires deprecated lifecycle state;
- values other than explicit string `"true"` or `"false"` for `skillet.deprecated` are invalid.

Issue #38 adds generic lifecycle governance independently of that shorthand. A capability may therefore be deprecated without a successor using:

```yaml
metadata:
  skillet.governance.state: "deprecated"
  skillet.governance.reason: "no longer recommended for new work"
```

When `skillet.replaced_by` is present, the capability must be deprecated whether that state came from the legacy shorthand or `skillet.governance.state`.

## Catalogue projection

These keys are **control metadata**, not routing content.

The implementation must:

- preserve governance declarations even when `search.searchable_metadata_keys` filters ordinary metadata;
- project lifecycle/successor state into dedicated catalogue capability fields;
- exclude control keys from routing text, embedding input, and generic relevance scoring;
- keep successor lineage presentation-only;
- report whether the target currently resolves when that can be established without changing selection semantics.

This avoids a valid metadata-filter configuration accidentally erasing replacement guidance, and avoids the words in governance values affecting search relevance.

## Selection and materialisation behaviour

Skillet must never silently substitute the replacement.

When a deprecated skill is surfaced:

- discovery clients expose that it is deprecated and identify the declared replacement when present;
- a consuming agent or user may choose the replacement through the normal explicit selection flow;
- an explicit request to materialize a retained deprecated skill remains valid when the requested revision is otherwise available;
- lock restoration continues to restore the exact locked historical revision rather than following replacement lineage.

Yanked revisions are different: they are excluded from normal discovery and new selection/materialisation, while exact pinned lock restoration remains available for reproducibility when the immutable package is retained.

The bundled `find-skills` workflow may recommend considering the replacement, but it must not transform one candidate ID into another behind the user's back.

## Trust and relationship boundary

Successor lineage is publisher-declared evidence of maintainer intent. It does not:

- prove behavioural equivalence;
- imply that the replacement is compatible with every task, model, or harness that used the old skill;
- create a dependency or invocation edge;
- authorize the old skill to execute the replacement;
- change retrieval ranking initially;
- auto-upgrade lockfiles or active host state;
- derive `replaced_by` from semantic-neighbour similarity or co-use telemetry.

If replacement information later affects ranking or automatic policy, that requires separate evaluation and an explicit decision.

## Implementation

The governance implementation provides:

1. validation of successor and lifecycle control metadata;
2. typed lifecycle/successor projection in capability discovery and description;
3. exclusion of governance keys from routing text and embedding input;
4. explicit selection with no automatic successor substitution;
5. regression coverage for historical revision materialisation/restoration, unresolved replacement targets, yanked selection denial, and scope-safe successor references.

No database migration is required merely to duplicate current governance state because it is derived from source-controlled metadata on immutable admitted revisions. If governance is later persisted independently of source metadata, state changes must gain append-only audit evidence rather than rewriting historical records.

## Consequences

Skillet can address cross-ID drift without weakening reproducibility or becoming an upgrade manager. SemVer remains responsible for evolution within a stable skill identity; lifecycle governance controls eligibility; and `replaced_by` communicates publisher intent when the maintained successor has a different identity.
