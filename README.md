# Skillet

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
