# Scoped capability discovery

Skillet vNext models reusable task guidance as a **capability** while retaining the existing Agent Skills catalogue, immutable revisions, packages, and `search_skills` / `materialize_skill` compatibility surface.

## Capability boundary

A capability descriptor is intentionally compact. It contains stable identity, kind, routing description, compatibility, trust/status metadata, scope, source, and immutable provenance. Full skill contents remain behind explicit materialisation. Existing admitted Agent Skills project into `kind: skill`; `playbook` is reserved as a materialised capability kind and does not add workflow execution.

Capabilities and knowledge are separate domains. Capability search answers **what reusable procedure should help with this task?** Knowledge retrieval answers **what organisational facts or context are relevant?** They are not cross-ranked.

## Scope

Visibility is hierarchical:

- organisation-wide sources are central and visible throughout the authenticated organisation;
- namespace-scoped sources are visible only when the request uses the same namespace;
- repository-scoped sources are visible only when both namespace and repository match exactly.

Scope is eligibility policy, not routing text. Namespace and repository identifiers are not appended to descriptions or embeddings and do not add a locality ranking boost. A more relevant central capability may therefore outrank an eligible local capability.

Existing repository configuration remains central by default. To make a source local, configure `capability_scope`:

```yaml
repositories:
  - id: shared-skills
    url: https://github.com/example/shared-skills.git
    ref: refs/heads/main
    poll_interval: 15m

  - id: payments-local-skills
    url: https://github.com/example/payments.git
    ref: refs/heads/main
    poll_interval: 15m
    capability_scope:
      namespace: payments
      repository: checkout-api
```

For a namespace-wide source, omit `repository`:

```yaml
capability_scope:
  namespace: payments
```

A repository scope without a namespace is invalid. Traversal-like or malformed scope identifiers are rejected. Organisation identity continues to come from the authenticated server context; namespace/repository scope is discovery context and must not be treated as a replacement for repository authorization policy.

## MCP surface

`search_capabilities` is the vNext scoped discovery surface. It accepts a natural-language query plus optional namespace/repository context and returns compact candidate descriptors with signed candidate IDs. The candidate still requires explicit selection.

`describe_capability` resolves one selected candidate to the same immutable revision and exposes its provenance and materialisation linkage. For `kind: skill`, the existing `materialize_skill` path remains authoritative. Describe does not select a successor, install content, execute scripts, or mutate source state.

The existing `search_skills` surface remains available. Because it has no namespace/repository input, explicitly scoped sources are excluded from its routing index; central sources retain existing v1 behaviour. This prevents local-only capabilities from becoming visible through the compatibility path.

## Verification

`go run ./cmd/skillet-verify` includes the protected scoped-capability corpus and the offline MCP workflow. The gate records:

- capability top-1 accuracy;
- recall@3;
- multi-capability recall@5;
- negative false-activation rate;
- scope leakage, required to remain `0` for the protected corpus.

The offline workflow admits one central source and two repository-local sources with overlapping names/intents, verifies bidirectional isolation, verifies central-only behaviour when repository context is absent, rejects malformed scope, and materialises both central and local selections through the existing immutable skill package path.
