# Scoped capability discovery

Skillet vNext models reusable task guidance and tool metadata as a **capability** while retaining the existing Agent Skills catalogue, immutable revisions, packages, and `search_skills` / `materialize_skill` compatibility surface.

## Capability boundary

A capability descriptor is intentionally compact. It contains stable identity, kind, routing description, compatibility, trust/status metadata, scope, source, and provenance. Existing admitted Agent Skills project into `kind: skill`; `playbook` remains a non-executing reusable-procedure kind; metadata imported from configured MCP catalogues projects into `kind: tool`.

Full skill contents remain behind explicit materialisation. Full MCP input schemas remain behind explicit `describe_capability` selection. Tool descriptions and schemas are treated as untrusted metadata: importing or describing them does not interpret them as Skillet instructions, register them as invokable Skillet tools, acquire delegated credentials, or execute a remote tool.

Capabilities and knowledge are separate domains. Capability search answers **what reusable procedure or tool could help with this task?** Knowledge retrieval answers **what organisational facts or context are relevant?** They are not cross-ranked.

## Scope

Visibility is hierarchical:

- organisation-wide sources are central and visible throughout the authenticated organisation;
- namespace-scoped sources are visible only when the request uses the same namespace;
- repository-scoped sources are visible only when both namespace and repository match exactly.

Scope is eligibility policy, not routing text. Namespace and repository identifiers are not appended to descriptions or embeddings and do not add a locality ranking boost. Capability kind is also not a ranking boost: an eligible skill, playbook, or tool wins only on retrieval relevance.

Existing repository configuration remains central by default. To make a skill source local, configure `capability_scope`:

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

## MCP tool metadata catalogues

A tool catalogue is a deterministic JSON metadata snapshot. Skillet reads it at startup; the runtime does not connect to the described MCP server. Configuration intentionally contains no endpoint token, delegated credential, or execution flag:

```yaml
mcp_tool_catalogues:
  - id: shared-github-tools
    path: ./catalogues/github-tools.json
    trust_level: approved

  - id: checkout-tools
    path: ./catalogues/checkout-tools.json
    trust_level: approved
    capability_scope:
      namespace: payments
      repository: checkout-api
```

Snapshot format:

```json
{
  "version": 1,
  "server": {
    "id": "github",
    "title": "GitHub MCP",
    "source": "catalogue://github",
    "compatibility": "mcp-2025-06-18",
    "auth": "host-provided"
  },
  "tools": [
    {
      "name": "search_issues",
      "title": "Search issues",
      "description": "Search GitHub issues by repository, labels, state and assignee",
      "input_schema": {
        "type": "object",
        "properties": {"query": {"type": "string"}},
        "required": ["query"],
        "additionalProperties": false
      }
    }
  ]
}
```

Stable tool identity is derived from `server.id + tool.name`. A separate deterministic revision changes when routing/detail metadata changes. Duplicate stable identities across configured catalogues are rejected rather than silently aliased. Schemas and snapshots are size-bounded; malformed schemas fail closed. The metadata provider retains its last successfully accepted in-memory snapshot if a later refresh source is unavailable.

The routing index contains only tool name, description, and compatibility. The full schema is canonicalized and hashed but is not part of routing text. Compact schema summary/digest metadata can be returned on the descriptor; the full canonical schema appears only in `describe_capability`.

## MCP surface

`search_capabilities` is the vNext scoped discovery surface. It accepts a natural-language query plus optional namespace/repository context and returns compact mixed-kind candidate descriptors with signed candidate IDs. The candidate still requires explicit selection.

`describe_capability` resolves one selected candidate to the same revision. For `kind: skill`, the existing `materialize_skill` path remains authoritative. For `kind: tool`, describe progressively discloses the full input schema as untrusted metadata and exposes no materialisation or execution linkage.

There is deliberately no generic `execute_capability` operation, no tool proxy in this slice, and discovered MCP tool names are not registered as invokable tools on Skillet's own MCP server. A later execution feature must define its own trust, authorization, credential and audit boundary rather than inheriting discovery authority.

The existing `search_skills` surface remains available. Because it has no namespace/repository/kind input, explicitly scoped sources are excluded from its routing index and MCP tool catalogue documents are never inserted into it; central skills retain existing v1 behaviour.

## Verification

`go run ./cmd/skillet-verify` includes the protected mixed capability corpus and runs the offline E2E suite as part of `go test ./...`. The capability report records:

- top-1 accuracy;
- recall@3;
- multi-capability recall@5;
- recall by capability kind (`skill`, `playbook`, `tool`);
- cross-kind top-result confusion counts;
- negative false-activation rate;
- scope leakage, required to remain `0` for the protected corpus.

The mixed offline workflow proves tool-best, skill-best, both-needed and none-needed tasks; full-schema progressive disclosure; absence of a tool-execution surface; repository-scope isolation; unchanged skill materialisation; and unchanged legacy `search_skills` behaviour.
