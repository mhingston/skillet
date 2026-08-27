# ADR-012: Cross-ID successor lineage is publisher-declared catalogue state

## Status

Accepted as the contract direction. Implementation should remain a small presentation/discovery slice and must not introduce automatic upgrade or workflow semantics.

## Context

Skillet already preserves immutable revisions and can resolve SemVer within one stable skill identity. That solves version drift when a maintained skill evolves under the same `skill_id`.

It does not solve a different form of drift: a maintainer may replace one skill with a differently named or differently located skill. In that case SemVer cannot tell a consumer that new work should normally use the successor, even though the older skill must remain reproducible for lock restoration and historical runs.

Semantic neighbours are also insufficient evidence for replacement. Similarity can indicate overlap or adjacency, but cannot establish maintainer intent.

## Decision

Skillet will support one explicit cross-ID successor relationship:

```yaml
metadata:
  skillet.deprecated: "true"
  skillet.replaced_by: "org/repository/new-skill"
```

The namespaced metadata keys are publisher declarations, not inferred relationships.

This first slice intentionally does not add `supersedes`, `alternative_to`, dependency, composition, or workflow-order semantics.

### Stable-identity semantics

Deprecation and replacement describe the **current catalogue state of the stable skill identity**, not the state of every historical package revision.

The declaration is read from the active source revision and projected onto the stable `skill_id` for discovery/presentation. Historical revisions remain immutable and retain their original package contents, commit/tree identity, digests, and restoration behaviour.

If a later active source revision removes the declaration, the current stable identity is no longer presented as replaced. Skillet does not rewrite historical revisions to reflect that change.

### Target identity

`skillet.replaced_by` names a stable Skillet skill ID, not a version or revision ID. The initial relationship is organisation-scoped and may cross repositories within the same organisation.

The source must not point to itself. A replacement target need not be present at the exact instant the source is admitted because repositories may synchronize independently. A missing target is therefore an unresolved catalogue reference to surface to maintainers/consumers, not a reason to mutate or silently redirect the source.

### Coupled declaration

For this first successor-lineage slice, the two fields are coupled:

- `skillet.deprecated: "true"` requires a non-empty `skillet.replaced_by`;
- `skillet.replaced_by` without `skillet.deprecated: "true"` is invalid for this contract;
- values other than explicit string `"true"` for `skillet.deprecated` are not treated as successor declarations.

Generic deprecation without a successor is a separate catalogue-governance problem and is deliberately not added through this relationship contract.

## Catalogue projection

These keys are **control metadata**, not routing content.

A future implementation must:

- preserve the declaration even when `search.searchable_metadata_keys` filters ordinary metadata;
- project it to dedicated `deprecated` and `replaced_by` fields in catalogue/search presentation;
- exclude the control keys from routing text, embedding input, and generic relevance scoring;
- keep the relationship presentation-only initially;
- report whether the target currently resolves when that can be established without changing selection semantics.

This avoids a valid metadata-filter configuration accidentally erasing replacement guidance, and avoids the words in a replacement ID affecting search relevance.

## Selection and materialisation behaviour

Skillet must never silently substitute the replacement.

When a deprecated skill is surfaced:

- discovery clients should expose that it is deprecated and identify the declared replacement;
- a consuming agent or user may choose the replacement through the normal explicit selection flow;
- an explicit request to materialize a retained deprecated skill remains valid when the requested revision is otherwise available;
- lock restoration continues to restore the exact locked historical revision rather than following replacement lineage.

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

## Implementation sequencing

The smallest implementation should add only:

1. validation of the two control metadata keys;
2. stable-identity projection into `list_skills` / `search_skills` output;
3. exclusion of the keys from routing text and ordinary searchable-metadata filtering;
4. bundled `find-skills` guidance to surface the replacement while preserving explicit selection;
5. regression coverage for historical revision materialisation/restoration and unresolved replacement targets.

No database migration is required merely to duplicate the declaration if current stable catalogue state can be derived from the active source revision. A migration should be introduced only if later evidence shows identity-level state must survive independently of source metadata.

## Consequences

Skillet can address cross-ID drift without weakening reproducibility or becoming an upgrade manager. SemVer remains responsible for evolution within a stable skill identity; `replaced_by` communicates publisher intent when the maintained successor has a different identity.
