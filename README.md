# Skillet

![Skillet](assets/skillet.webp)

Skillet is a remotely hosted organisational registry for Agent Skills. Agents search a compact catalogue over authenticated Streamable HTTP MCP, choose candidates, and acquire immutable packages outside the project working tree. Search results never contain complete skill packages.

The repository contains a runnable single-node vertical slice: strict configuration validation, SQLite WAL-backed catalogue state, configured Git polling, Agent Skills discovery/quarantine, deterministic package archives, content-addressed retention, Bleve-plus-vector routing retrieval, configurable embeddings and listwise reranking adapters, signed package URLs, locked restoration, OIDC/JWKS validation, MCP search/materialisation, audit events, and Prometheus counters.

## Local development

Go 1.25 and the pinned official MCP Go SDK are required.

```sh
go test ./...
go vet ./...
go run ./cmd/skillet -config skillet.example.yaml
```

The example starts in explicit development mode with no external database or model provider. Endpoints are `/healthz`, `/readyz`, `/metrics`, and `/mcp`. Production deployments must configure OIDC or static bearer authentication and signing keys.

## Product boundary

The service is intentionally single-node and SQLite-backed in v1. Approved repositories are configured by operators; admitted packages retain exact commits and SHA-256 digests. Skillet will not execute skill scripts, resolve skill dependencies, modify client harness directories, or install a companion client binary.

A compatible MCP host must already provide shell/download capability, outbound HTTPS, permission to write to a user cache outside the repository, and permission to read the extracted `SKILL.md`. Hosts without those capabilities may be search-only. Skillet is not marketed as universally compatible with every MCP client.

The remote materialisation flow returns a signed immutable package URL, a fixed POSIX or PowerShell acquisition command, an external cache destination, and a deterministic `skillet-lock.json` entry. The server never writes to the MCP client filesystem and never executes skill-provided scripts.

See [the implementation handoff](docs/implementation-handoff.md) and [architecture decisions](docs/adr/) for the bounded roadmap and security decisions.

## How it works

1. An operator configures approved Git repositories and starts the service.
2. Skillet polls those repositories, discovers valid `SKILL.md` directories, and retains immutable packages for admitted revisions.
3. An agent calls `search_skills` and receives a small set of metadata-only candidates.
4. The agent or harness selects a candidate and calls `materialize_skill`.
5. Skillet returns a short-lived package URL, an integrity digest, a fixed acquisition command, and a lockfile entry.
6. The host downloads and verifies the package in its external cache, then reads `SKILL.md` when the skill is needed.

Skillet never silently selects or installs a skill, puts package contents into ordinary MCP results, executes skill scripts, or writes to the client filesystem.

## Trust and security boundary

Skillet is a distribution and retrieval service for repositories that an organisation has already approved. Repository admission is configuration-controlled in v1; there is no public submission or marketplace workflow.

The service protects the distribution path with exact Git commits, specification validation, safe deterministic packaging, SHA-256 integrity, durable historical retention, organisation-scoped authentication, short-lived package URLs, and audit events. Source repositories, skill metadata, and package files remain untrusted input to the service and are never treated as instructions by the retrieval pipeline.

Skillet does not claim to certify that a skill is safe to execute. It does not execute scripts, enforce `allowed-tools`, sandbox a consuming agent, or replace an organisation's source-repository review, CI controls, marketplace approval, endpoint protection, or runtime authorization. Those controls belong upstream and in the consuming host. In particular, a prompt that asks an agent to request confirmation is not an enforcement boundary; real authorization must be implemented by the host or infrastructure.

## Retrieval evaluation

The repository includes a deterministic 50-case routing evaluation under
[`evals/`](evals/). It contains routing metadata and synthetic vectors only;
the external `mhingston/agent-skills` repository is intentionally not vendored.

```sh
go run ./cmd/skillet-eval --fixtures evals/retrieval.yaml
```

The evaluator enforces the handoff thresholds for top-1 accuracy, recall@3,
multi-skill recall@5, negative false activation, and baseline regression.

The real demonstration-corpus admission/search test is opt-in and never
vendors the corpus:

```sh
SKILLET_EXTERNAL_CORPUS=1 go test ./internal/e2e -run TestExternalAgentSkillsCorpus
```

The remote MCP journey, including command execution and locked restoration,
is covered by `TestRemoteMCPSearchMaterializeExecuteAndRestore`.

## Future work

The v1 service intentionally stops at a coherent single-node registry. The following work is relevant to Skillet but is not required for the current release:

- **Repository and catalogue governance:** human admission workflows, ownership and maintainer records, quarantine review, revocation, deprecation, stale-description detection, duplicate and overlap analysis, and periodic catalogue audits.
- **Retrieval quality:** richer metadata filters, hierarchical organisation, diversity-aware ranking, query rewriting, learning-to-rank, larger-scale vector indexes, and feedback from human relevance judgements.
- **Distribution and provenance:** package signatures or attestations, object-storage-backed packages, formal manifests, publisher identity, cross-registry federation, and stronger restoration policies.
- **Security integrations at the trust boundary:** optional integration with an organisation’s existing repository or marketplace scanners, policy engines, SARIF pipelines, and approval records. Skillet should consume trusted decisions rather than become a general-purpose malware scanner or execution sandbox.
- **Lifecycle and compatibility:** per-skill versions, changelogs, breaking-change guidance, compatibility records across models and hosts, replacement skills, and retained-version policies.
- **Client integrations:** native host materialisation, automatic resource-link downloads, harness-specific activation adapters, and lockfile maintenance by capable MCP hosts.
- **Operations and scale:** PostgreSQL or object storage adapters, distributed search, horizontal deployment, backup/restore tooling, richer dashboards, and additional MCP client compatibility testing.

These items should be driven by pilot evidence. Skill execution, dependency resolution, automatic activation, public submissions, workflow orchestration, and a general-purpose security marketplace remain outside Skillet’s product boundary unless that boundary is deliberately revisited.

## Container deployment

For a local single-node deployment:

```sh
docker compose up --build
```

The example configuration is development-only. Production deployments must
replace it with OIDC or static bearer authentication, HTTPS, signing keys,
approved repositories, and a persistent data directory.

The supplied Compose file uses a named volume because the image runs as the
unprivileged UID 65532. If replacing it with a host bind mount, make the host
data directory writable by UID 65532 before starting the service.
