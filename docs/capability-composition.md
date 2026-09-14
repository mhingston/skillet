# Deterministic capability composition

Skillet supports a deliberately small composition model for capabilities that are commonly used together. Composition is declarative planning only: it does not add a workflow engine, execute tools, activate skills, infer dependencies, or let model output become dependency metadata.

## Source-authored dependency metadata

Dependencies remain inside the standard Agent Skills `metadata` map so `SKILL.md` keeps the existing strict top-level contract. The three reserved values are JSON arrays because Agent Skills metadata values are strings:

```yaml
---
name: incident-review
description: Review an incident using the approved engineering process.
metadata:
  version: 2.1.0
  skillet.requires: '[{"id":"demo/platform/evidence-review","version":"^1.4.0","kind":"skill"}]'
  skillet.recommends: '[{"id":"demo/platform/architecture-context","version":">=1.0.0 <2.0.0"}]'
  skillet.conflicts: '[{"id":"demo/platform/legacy-review","version":"<3.0.0"}]'
---
```

Each reference has a required stable `id` and may constrain `revision_id`, `version`, `kind`, `namespace`, and `repository`. `revision_id` and `version` are mutually exclusive. `version` accepts an exact strict SemVer or a SemVer range. As with `resolve_skill`, prerelease revisions are considered only when the selector explicitly mentions a prerelease.

`requires` expands the deterministic closure. `recommends` is advisory and is never selected automatically. `conflicts` rejects a plan when a selected immutable revision matches the declaration. Unknown or malformed composition metadata is rejected during skill admission as `invalid_composition`.

## Curated collections

Collections are source-controlled membership lists. They intentionally have no ordering semantics, branches, conditions, retries, variables, hooks, prompts, or automatic invocation.

Set `SKILLET_COLLECTIONS_FILE` to a version-controlled YAML manifest available to the Skillet process:

```yaml
version: 1
collections:
  - id: incident-review-kit
    name: Incident review kit
    description: Capabilities approved for incident review.
    members:
      - id: demo/platform/incident-review
        version: "^2.0.0"
      - id: demo/platform/postmortem-template
```

The parser uses strict known fields, limits a manifest to 64 collections and a collection to 128 members, rejects duplicate collection IDs, and normalises member ordering. Tests can attach the same collection model directly with `Server.ConfigureCollections`; this does not create a second persistence model.

## Resolution semantics

`resolve_capability_plan` accepts exactly one explicit capability selection (`candidate_id`) or collection. The browser preview also supports an already-authorised immutable `revision_id` so the catalogue UI can inspect a known revision without producing a new short-lived candidate.

Resolution is deterministic and side-effect free:

1. Use only declared `requires` edges or curated collection membership. Semantic search and LLM inference never create edges.
2. Resolve a stable identity against authorised, governance-eligible immutable catalogue revisions.
3. Intersect all exact/range constraints reaching the same identity and deterministically prefer the highest compatible SemVer. If that choice introduces an incompatible transitive constraint, backtrack deterministically to the next compatible revision.
4. Reject unsatisfiable constraints, required cycles, and selected conflicts. Deduplicate a dependency reached through multiple paths while preserving all explanatory edges.
5. Emit dependencies before dependants and sort explanatory metadata deterministically.
6. Lock the exact stable identity, kind, version, immutable revision, commit, tree, available package digests, materialisation method, and governance state in the returned plan.

Deprecated revisions may still be selected when an explicit constraint resolves to them; replacement metadata is presentation guidance only and is never followed silently. Yanked or explicitly unapproved revisions are unavailable for new composition selection. Existing exact lock restoration remains a separate path and is unchanged: restoring a previous lock does not re-resolve composition or follow successor guidance.

## Authorization and disclosure

Authorization and scope are eligibility gates, not ranking or resolution inputs. Before Skillet fetches the candidate revisions or dependency metadata for a stable dependency identity, it resolves the authoritative capability scope and applies the existing `capability.describe` authorization check. A denied or invisible dependency is omitted from the resolver snapshot; the resulting failure is deliberately generic and does not disclose its identity, version, repository, or metadata.

The closure is bounded to 128 stable identities and 512 immutable revisions per request. Repository/namespace scope rules apply exactly as they do to capability discovery. A caller-supplied scope cannot widen visibility.

## Surfaces

The MCP tool `resolve_capability_plan` returns the machine-readable immutable plan and performs no execution or materialisation. The browser route `/ui/composition` lists configured collection names, while `/ui/composition/preview` renders the same resolver output with immutable locks, declared edge reasons, recommendations, and conflict declarations. Both surfaces explicitly label the result as a preview.

Capability detail already exposes source-authored metadata, including `skillet.requires`, `skillet.recommends`, and `skillet.conflicts`; the composition preview turns those declarations into the authorised resolved plan before any materialisation step.

## Deliberate non-goals

This milestone does not provide workflow orchestration, auto-composition from model output, self-modifying capability metadata, background execution, automatic materialisation, tool invocation, sequencing, retries, approval flows, or hidden replacement/substitution. Those would require separate semantics, authorization boundaries, and evaluation evidence rather than being smuggled into dependency resolution.
