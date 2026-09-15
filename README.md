# Skillet

![Skillet](assets/skillet.webp)

> A self-hosted registry and discovery service for reusable AI agent skills and organisational knowledge.

Skillet gives AI agents one place to find approved skills, retrieve relevant organisational knowledge, and materialise the exact skill revision they need without loading an entire catalogue into context.

It exposes an MCP endpoint for agent hosts and a small `skillet-client` CLI for search and materialisation. Skillet keeps provenance, versions, governance and evidence explicit; it does **not** execute discovered skills or tools on the agent's behalf.

## Quick start

The fastest way to try Skillet is from source with Go 1.25.

```sh
git clone https://github.com/mhingston/skillet.git
cd skillet
cp skillet.example.yaml skillet.yaml
```

Edit `skillet.yaml` and point it at a repository containing Agent Skills:

```yaml
server:
  listen: ":8080"
  data_dir: "./.skillet-data"
  public_base_url: "http://localhost:8080"
  mcp_path: "/mcp"

organization:
  id: "demo"
  display_name: "Demo Organisation"

auth:
  mode: "development"

packages:
  enabled: true

search:
  default_limit: 5
  max_limit: 10

repositories:
  - id: "my-skills"
    url: "https://github.com/your-org/skills.git"
    ref: "refs/heads/main"
    poll_interval: "1m"
    trust_level: "approved"
    owner: "your-team"
```

Start Skillet:

```sh
go run ./cmd/skillet -config skillet.yaml
```

The MCP endpoint is now available at:

```text
http://localhost:8080/mcp
```

You can also browse the human-facing catalogue at `http://localhost:8080/ui`.

Check that discovery works:

```sh
go run ./cmd/skillet-client search \
  -query "plan a safe database migration"
```

The response contains compact candidates rather than full skill contents. Once you choose a candidate, materialise it into your agent host's skill directory:

```sh
go run ./cmd/skillet-client materialize \
  -candidate <candidate-id> \
  -destination ~/.claude/skills
```

Use the equivalent skill directory for your host, for example `~/.codex/skills`, `~/.copilot/skills`, or `~/.config/opencode/skills`.

## Connect an agent

Configure your MCP-capable agent host to use:

```text
http://localhost:8080/mcp
```

Skillet also includes a small bootstrap skill at [`skills/find-skills/SKILL.md`](skills/find-skills/SKILL.md). Installing it gives compatible hosts a reusable workflow for deciding when and how to query Skillet:

```sh
cp -R skills/find-skills ~/.claude/skills/find-skills
# or ~/.codex/skills/find-skills
# or ~/.copilot/skills/find-skills
# or ~/.config/opencode/skills/find-skills
```

A useful host-level instruction is simply:

```text
For specialised or repository-level work, search Skillet before choosing a task-specific skill. Prefer a relevant approved result when one is available.
```

Skillet returns metadata first. The caller explicitly chooses what to materialise; Skillet never silently selects or executes a capability.

## What Skillet gives you

- **Skill discovery** — natural-language search across approved Agent Skills without injecting every skill into the model context.
- **Progressive disclosure** — compact search results first, full skill materialisation only after selection.
- **Reproducibility** — immutable revisions, Git provenance, package digests and lockable materialisations.
- **Organisational knowledge** — separate Markdown/OKF knowledge search so facts and reusable procedures are not mixed into one ranking model.
- **Governance and scope** — active/deprecated/yanked lifecycle plus organisation, namespace and repository visibility controls.
- **MCP capability metadata** — discover metadata for external tools without turning Skillet into a tool-execution broker.
- **Human review and evidence** — optional collaboration, feedback, improvement proposals and evidence-driven capability evolution while canonical source remains review-controlled.

## Sources

A skill source can be a remote Git repository:

```yaml
repositories:
  - id: shared-skills
    url: https://github.com/example/shared-skills.git
    ref: refs/heads/main
    poll_interval: 15m
    trust_level: approved
    owner: platform
```

Or a local directory:

```yaml
repositories:
  - id: local-skills
    path: /absolute/path/to/skills
    poll_interval: 1m
    trust_level: approved
    owner: local
```

Repositories are central to the organisation by default. For namespace- or repository-scoped catalogues, see [`docs/scoped-capabilities.md`](docs/scoped-capabilities.md).

## Running with Docker

The repository includes a `Dockerfile` and `compose.yaml`:

```sh
docker compose up --build
```

The supplied Compose configuration mounts `skillet.example.yaml`, so configure your sources there before starting it. For production, use persistent storage and replace `auth.mode: development` with static or OIDC authentication.

Prebuilt server and client binaries for supported platforms are also published on the [GitHub Releases](https://github.com/mhingston/skillet/releases) page.

## Production basics

`auth.mode: development` is for local use only. Production deployments should use `static` or `oidc`, HTTPS, persistent storage, explicit signing keys, and approved sources.

Skillet can enforce provider-neutral claims-based authorisation and supports an Entra-compatible OIDC profile without requiring Microsoft Graph at runtime. See [`docs/enterprise-operations.md`](docs/enterprise-operations.md).

Skillet is deliberately a **registry, retrieval, distribution and evidence control plane**. It does not:

- execute skill scripts or discovered MCP tools;
- become an agent workflow engine;
- train or fine-tune models;
- silently alter semantic ranking from feedback;
- automatically rewrite or promote canonical source;
- allow an improving system to rewrite its own protected evaluator.

External improvement runners, when enabled, execute outside Skillet and return signed evidence bound to the exact experiment. See [`docs/m4-release-gate.md`](docs/m4-release-gate.md).

## Useful commands

Search:

```sh
go run ./cmd/skillet-client search -query "review this pull request"
```

Materialise a selected candidate:

```sh
go run ./cmd/skillet-client materialize \
  -candidate <candidate-id> \
  -destination ~/.codex/skills
```

Run the full deterministic verification suite:

```sh
go run ./cmd/skillet-verify
```

Run the normal Go checks:

```sh
go test ./...
go vet ./...
```

## Documentation

Start with these when you need more than the quick path above:

- [`docs/scoped-capabilities.md`](docs/scoped-capabilities.md) — capability sources, scope and lifecycle.
- [`docs/okf-knowledge.md`](docs/okf-knowledge.md) — organisational knowledge and OKF ingestion.
- [`docs/capability-composition.md`](docs/capability-composition.md) — declared dependencies and deterministic lock plans.
- [`docs/claude-code-marketplace-distribution.md`](docs/claude-code-marketplace-distribution.md) — Claude Code distribution profile.
- [`docs/enterprise-operations.md`](docs/enterprise-operations.md) — production authentication, authorisation and operations.
- [`docs/m4-release-gate.md`](docs/m4-release-gate.md) — optional capability-evolution and external-runner boundaries.
- [`docs/implementation-handoff.md`](docs/implementation-handoff.md) — architecture, verified product boundaries and implementation detail.

## Status

The current implementation has deterministic acceptance coverage across discovery/materialisation, organisational knowledge, enterprise controls, human/adoption workflows and the opt-in capability-evolution control plane. The authoritative repository-wide gate is:

```sh
go run ./cmd/skillet-verify
```

Release-gate and architecture detail intentionally lives under [`docs/`](docs/) rather than in this README so the main page stays focused on getting a user from zero to a working Skillet instance.