# Capability governance

Skillet vNext treats governance as control-plane metadata around an immutable capability revision. Git repositories and pull requests remain the contribution and review workflow; M1 does not introduce a marketplace, identity directory, proposal queue, or admin UI.

## Ownership and visibility

Repository configuration provides the default owner and capability scope:

```yaml
repositories:
  - id: central
    owner: platform

  - id: checkout-skills
    owner: checkout
    capability_scope:
      namespace: payments
      repository: checkout
```

A capability may override presentation ownership with reserved metadata:

```yaml
metadata:
  skillet.governance.owner: "payments-platform"
  skillet.governance.maintainers: "alice,bob"
```

Visibility is derived from the configured source scope and is reported as `organization`, `namespace`, or `repository`. Scope is an eligibility rule only. It never boosts relevance.

## Lifecycle states

`active` is the default state. The explicit state key is:

```yaml
metadata:
  skillet.governance.state: "active" # active | deprecated | yanked
```

The lifecycle policy is:

| State | Normal discovery | New selection/materialization | Exact locked restore |
| --- | --- | --- | --- |
| `active` | yes, when approved/searchable and in scope | yes | yes |
| `deprecated` | yes, with warning/replacement metadata | yes; caller explicitly selects the deprecated revision | yes |
| `yanked` | no | no | yes, when an exact retained immutable revision and digest are pinned |

A yank is terminal for that immutable revision. Publishing a corrected capability creates another immutable revision; governance does not rewrite historical revision or package data.

`skillet.governance.reason` can carry a short maintainer-supplied explanation. Governance is source-controlled rather than persisted in a separate mutable state service, so Git history is the review/audit trail for governance changes while catalogue revision history remains immutable.

## Deprecation and successor lineage

The existing successor contract remains supported:

```yaml
metadata:
  skillet.deprecated: "true"
  skillet.replaced_by: "demo/central/new-skill"
```

`skillet.deprecated: "true"` requires a non-empty `skillet.replaced_by`; `replaced_by` without deprecation is invalid. The equivalent typed state is `deprecated`. Contradictory typed and legacy declarations are rejected rather than resolved by precedence.

Successor lineage is guidance, never an alias:

- search/describe continue to identify the selected deprecated revision;
- materialization never silently switches to the successor;
- lock restoration always resolves the exact locked revision and digest;
- an unresolved same-organisation successor can be reported as unresolved guidance without corrupting catalogue state;
- self-replacements and cross-organisation replacements are rejected;
- when the successor is currently known, its visibility must be at least as broad as the deprecated source capability, so successor guidance cannot escape or narrow the caller's scope.

## Discovery and relevance

Normal `search_capabilities` discovery defaults to approved sources. Searchability, scope, lifecycle state, and approval are eligibility/control decisions. The following reserved keys are excluded from lexical routing text, embedding input, and model-reranker candidate metadata:

- `skillet.governance.state`
- `skillet.governance.owner`
- `skillet.governance.maintainers`
- `skillet.governance.reason`
- `skillet.deprecated`
- `skillet.replaced_by`

Changing ownership, maintainers, lifecycle state, reason, visibility, or successor guidance therefore cannot change semantic relevance unless a future retrieval policy explicitly introduces and evaluates such a signal.

When `search.searchable_metadata_keys` is configured, Skillet still retains these reserved governance keys for control-plane projection while excluding them from every relevance input.

## Reproducibility boundary

Governance affects new discovery and selection, not immutable provenance. A yanked revision is removed from new capability discovery, but its admitted revision and package remain available to the exact locked-restore path. `replaced_by` is never followed during restore.

This preserves the core invariant: a lockfile restores the same revision, commit, tree, archive digest, and package bytes that were originally pinned, regardless of later ownership, deprecation, successor, or yank decisions.
