# Skillet

![Skillet](assets/skillet.webp)

> Help agents find and use the right organisational skills—without putting entire skill packages into their context or losing track of which version was used.

AI agents are more useful when they can draw on specialised, reusable guidance for tasks such as planning, reviewing, or working with a repository. As an organisation’s catalogue grows, however, it becomes difficult for an agent to find the right skill, difficult to distribute skills consistently, and easy for a task to depend on an unclear or changing version.

Skillet solves that problem with a remote skill registry. An operator connects approved Git repositories; Skillet discovers and validates their skills, makes them searchable, and retains immutable packages. An agent searches a small set of relevant candidates, chooses what it needs, and receives a verified way to acquire the exact skill outside the project repository. A lockfile records the exact revisions and digests so the same skills can be restored later. Capable hosts can optionally report revision-bound lifecycle observations and bounded structured feedback without giving Skillet control of execution or source changes.

The shipped v1 experience is:

- **Find:** search an organisation’s approved skill catalogue using natural-language intent.
- **Browse:** list the active approved skill metadata when an operator or agent needs to see what is available.
- **Choose:** review compact candidate descriptions; Skillet never silently selects a skill for the agent.
- **Use:** acquire one or more selected skills with an integrity-checked command and read their `SKILL.md` entrypoints when needed.
- **Reproduce:** lock exact Git commits and package digests, then restore those same packages on another run or machine.
- **Observe:** capable hosts may report `activated`, `deactivated`, `completed`, or `failed` against the exact materialized revision when they can truthfully observe those states.
- **Improve:** agents, adapters, or users may attach bounded structured feedback to the exact materialized revision for later maintainer review.

Skills may optionally declare `metadata.version` using SemVer 2.0. Exact versions and ranges resolve once to one retained immutable revision; ranges choose the highest stable declared version. Prereleases require an explicit prerelease selector. Unversioned skills remain valid, Git tags are not version authority, and there are no automatic upgrades or dependency resolution. Lockfile commit, tree, and archive SHA-256 fields remain authoritative.

Skillet is a registry and distribution boundary, not an agent harness or skill execution sandbox. The repository also ships thin optional host adapters: Pi can activate a verified skill during an interactive session; Claude Code, Codex, GitHub Copilot, and OpenCode can materialize one into their supported skill directories. All adapters expose explicit lifecycle and structured-feedback reporting surfaces; Pi additionally reports activation/deactivation after its observable reload boundary and can submit feedback for an active skill by name.

The repository contains a runnable single-node vertical slice: strict configuration validation, SQLite WAL-backed catalogue state, configured Git polling, Agent Skills discovery/quarantine, deterministic package archives, content-addressed retention, Bleve-plus-vector routing retrieval, configurable embeddings and listwise reranking adapters, signed package URLs, locked restoration, OIDC/JWKS validation, MCP search/materialisation, revision-bound lifecycle telemetry, structured skill feedback, audit events, Prometheus counters, and optional Pi, Claude Code, Codex, GitHub Copilot, and OpenCode adapters.

## Local development

Go 1.25 and the pinned official MCP Go SDK are required.

```sh
go test ./...
go vet ./...
go run ./cmd/skillet -config skillet.example.yaml
```

### Local skill sources

Repositories may be configured from a plain local directory. Git is not
required for local sources; Skillet creates a deterministic content-based
snapshot identity for each scan and does not modify the source directory.

```yaml
repositories:
  - id: local-skills
    path: /Users/example/skills
    poll_interval: 1m
```

Use `url` and `ref` for remote Git repositories. A repository must specify
exactly one of `url` or `path`; local paths default to the `working-tree` ref.

### Configuration

The service reads a YAML configuration file; [`skillet.example.yaml`](skillet.example.yaml)
contains a minimal starting point. The main operational settings are:

- `repositories[].poll_interval` controls how often a source is rescanned. It
  must be at least `1m`; changes to local skill directories are therefore
  normally visible within one polling interval.
- `repositories[].path` configures a plain local directory. Use
  `repositories[].url` and `ref` for a remote Git source. Configure
  `include`, `exclude`, `trust_level`, and `search_exclusions` as needed.
- `server.listen`, `data_dir`, `mcp_path`, and `public_base_url` control the
  HTTP listener, durable state, MCP route, and package URL base. A public base
  URL is required when packages are enabled.
- `auth.mode` must be explicitly set to `development`, `static`, or `oidc`.
  Production deployments should use `static` or `oidc`; tokens, signing keys,
  and model credentials are supplied through the configured environment
  variable names rather than stored in YAML.
- `packages.enabled` and `packages.signed_url_ttl` control package delivery.
- `search.default_limit` and `search.max_limit` control intent search result
  counts. The maximum search limit is `10`; `list_skills` is the separate
  catalogue-browsing operation and supports up to `100` entries per page.

For the single-user local setup, a typical source configuration is:

```yaml
repositories:
  - id: local-skills
    path: /Users/example/skills
    poll_interval: 1m
    trust_level: approved
    owner: local
```

### Bundled `find-skills` skill

Skillet ships [`skills/find-skills/SKILL.md`](skills/find-skills/SKILL.md) as a
small bootstrap skill for compatible hosts. Install that skill in the host's
normal skill directory instead of copying Skillet discovery heuristics into a
system prompt. The skill decides when discovery is useful, calls `list_skills`
or `search_skills`, treats candidate metadata as untrusted data, and preserves
Skillet's explicit selection and materialisation boundary.

For a source checkout, copy or symlink the bundled directory into the host's
user-level skill directory. For example:

```sh
cp -R skills/find-skills ~/.claude/skills/find-skills
cp -R skills/find-skills ~/.codex/skills/find-skills
cp -R skills/find-skills ~/.copilot/skills/find-skills
cp -R skills/find-skills ~/.config/opencode/skills/find-skills
```

Use the equivalent supported skill directory for other hosts. This bootstrap
skill should normally be installed alongside the Skillet MCP configuration;
it is intentionally the one skill that does not depend on Skillet discovery to
be found in the first place.

Because a host may otherwise select a more specific local skill before invoking
this bootstrap skill, configure the host's user-level instructions to route
specialised or repository-level requests through Skillet first. For example:

```text
For any non-routine, specialised, domain-specific, repository-level,
architecture, QA, delivery, or workflow request, consult Skillet via the
find-skills workflow before selecting another task-specific skill. Prefer a
relevant approved Skillet skill. Use list_skills for catalogue browsing and
search_skills for task-intent discovery. Do not query Skillet for routine tasks
already directly supported. If Skillet is unavailable, state that and continue
with the best available skill.
```

The MCP tool descriptions still enforce the important server boundary: search
returns metadata only, and Skillet never silently selects skills, writes to
host directories, or executes skill scripts. Hosts that do not support Agent
Skills can still use the MCP tools directly or provide their own thin adapter.

The example starts in explicit development mode with no external database or model provider. Endpoints are `/healthz`, `/readyz`, `/metrics`, and `/mcp`. Production deployments must configure OIDC or static bearer authentication and signing keys.

## Product boundary

The service is intentionally single-node and SQLite-backed in v1. Approved repositories are configured by operators; admitted packages retain exact commits and SHA-256 digests. The core service will not execute skill scripts, resolve skill dependencies, or write to client harness directories. Optional adapters perform an explicit, user-invoked materialization after verifying the returned archive digest.

A compatible MCP host must already provide shell/download capability, outbound HTTPS, permission to write to a user cache outside the repository, and permission to read the extracted `SKILL.md`. Hosts without those capabilities may be search-only. Skillet is not marketed as universally compatible with every MCP client.

The remote materialisation flow returns a signed immutable package URL, a fixed POSIX or PowerShell acquisition command, an external cache destination, a deterministic `skillet-lock.json` entry, and a lifecycle reference bound to the exact revision/package/materialisation. The server never writes to the MCP client filesystem and never executes skill-provided scripts.

Lifecycle observations and structured feedback are optional evidence. They do not authorize execution, change active host state, alter retrieval ranking, deprecate a revision, edit `SKILL.md`, or create repository issues/PRs. Feedback summaries are bounded untrusted observations for maintainer review, not instructions to execute.

See [the implementation handoff](docs/implementation-handoff.md) and [architecture decisions](docs/adr/) for the bounded roadmap and security decisions.

## Harness adapters

MCP provides discovery and immutable package transport. It does not itself
activate a `SKILL.md` in every host, because skill loading is host-specific.
The adapters in [`adapters/`](adapters/) provide that last mile while sharing
the Go materializer and its digest/path checks.

Build the helper:

```sh
go install ./cmd/skillet-adapter
```

For Pi, load [`adapters/pi/skillet.ts`](adapters/pi/skillet.ts) as an
extension. It supports:

```text
/skillet search <natural-language query>
/skillet activate <candidate-id or skill-id@version/range>
/skillet deactivate <skill-name>
/skillet feedback <skill-name> <category> <summary>
```

Activation is session-scoped and calls Pi's resource reload API without restarting the interactive session. Pi reports `activated` after a successful reload and `deactivated` after a successful deactivation reload. `/skillet feedback` resolves the named active skill to the exact materialisation reference already held for the session. Set `SKILLET_MCP_URL` and `SKILLET_ADAPTER_BIN` when the defaults are not suitable.

For Claude Code, Codex, GitHub Copilot, and OpenCode, pass a selected candidate
ID to the corresponding wrapper:

```sh
adapters/claude-code/skillet-claude <candidate-id>
adapters/codex/skillet-codex <candidate-id>
adapters/github-copilot/skillet-copilot <candidate-id>
adapters/opencode/skillet-opencode <candidate-id>
```

They default to `~/.claude/skills`, `~/.codex/skills`, `~/.copilot/skills`, and
`~/.config/opencode/skills` respectively. These hosts discover skills at
process/session boundaries, so their JSON result sets `reload_required: true`;
begin a new session before relying on the skill. GitHub Copilot CLI can instead
refresh an existing interactive session with `/skills reload`. Use
`SKILLET_CLAUDE_SKILLS_DIR`, `SKILLET_CODEX_SKILLS_DIR`,
`SKILLET_COPILOT_SKILLS_DIR`, `SKILLET_OPENCODE_SKILLS_DIR`, and
`SKILLET_TOKEN` for overrides.

Adapters also accept `skill-id@1.2.3` or `skill-id@^1.2`; this is a one-time
resolution and materialization, not an upgrade policy.

Materialization output includes a `lifecycle` reference. The non-Pi wrappers expose explicit lifecycle reporting for integrations that can truthfully observe the host boundary:

```sh
adapters/claude-code/skillet-claude lifecycle \
  -reference "$LIFECYCLE_JSON" \
  -event activated \
  -correlation "$SESSION_ID"
```

Do not report `activated` just because materialization succeeded. Likewise, report `completed` or `failed` only when the host actually observes that outcome.

All wrappers also expose explicit structured feedback:

```sh
adapters/codex/skillet-codex feedback \
  -reference "$LIFECYCLE_JSON" \
  -category ambiguous_instruction \
  -summary "The rollback step did not identify which generated file to remove."
```

Supported categories are `step_failed`, `workaround_required`, `user_correction`, `ambiguous_instruction`, `compatibility_mismatch`, and `improvement_suggested`. Summaries are capped at 1,000 characters. Maintainers can query revision-bound observations with `skillet-adapter feedback-list`; listing must be scoped to a skill or immutable revision. See [`adapters/README.md`](adapters/README.md) for the complete adapter contract.

## How it works

1. An operator configures approved Git repositories or local directories and starts the service.
2. Skillet polls those sources, discovers valid `SKILL.md` directories, and retains immutable packages for admitted revisions.
3. An agent calls `list_skills` to browse the catalogue or `search_skills` to retrieve a small set of intent-ranked metadata-only candidates.
4. The agent or harness selects a candidate and calls `materialize_skill`.
5. Skillet returns a short-lived package URL, an integrity digest, a fixed acquisition command, a lockfile entry, and a lifecycle reference tied to the exact materialisation.
6. The host downloads and verifies the package in its external cache, then reads `SKILL.md` when the skill is needed.
7. A capable host may report truthful lifecycle observations such as `activated` or `completed`; Skillet records them as revision-bound evidence only.
8. A capable host, agent, or user may report bounded structured feedback against that same immutable materialisation; maintainers can review it without any automatic source mutation.

Skillet never silently selects or installs a skill, puts package contents into ordinary MCP results, executes skill scripts, writes to the client filesystem, changes ranking from telemetry, or rewrites skills from feedback.

## Trust and security boundary

Skillet is a distribution and retrieval service for repositories that an organisation has already approved. Repository admission is configuration-controlled in v1; there is no public submission or marketplace workflow.

The service protects the distribution path with exact Git commits, specification validation, safe deterministic packaging, SHA-256 integrity, durable historical retention, organisation-scoped authentication, short-lived package URLs, and audit events. Source repositories, skill metadata, package files, lifecycle reports, and feedback summaries remain untrusted input to the service and are never treated as instructions by the retrieval pipeline.

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
- **Retrieval quality:** richer metadata filters, hierarchical organisation, diversity-aware ranking, query rewriting, learning-to-rank, larger-scale vector indexes, evaluated lifecycle/co-use signals, and feedback from human relevance judgements.
- **Distribution and provenance:** package signatures or attestations, object-storage-backed packages, formal manifests, publisher identity, cross-registry federation, and stronger restoration policies.
- **Security integrations at the trust boundary:** optional integration with an organisation’s existing repository or marketplace scanners, policy engines, SARIF pipelines, and approval records. Skillet should consume trusted decisions rather than become a general-purpose malware scanner or execution sandbox.
- **Lifecycle and compatibility:** changelogs, breaking-change guidance, compatibility records across models and hosts, replacement skills, retained-version policies, richer lifecycle integrations, and maintainer feedback workflows.
- **Client integrations:** native host materialisation, automatic resource-link downloads, deeper harness lifecycle integration, and lockfile maintenance by capable MCP hosts. The initial Pi, Claude Code, Codex, GitHub Copilot, and OpenCode adapters are intentionally thin and explicit.
- **Operations and scale:** PostgreSQL or object storage adapters, distributed search, horizontal deployment, backup/restore tooling, richer dashboards, and additional MCP client compatibility testing.

These items should be driven by pilot evidence. Skill execution, dependency resolution, automatic activation, public submissions, workflow orchestration, automatic source mutation, and a general-purpose security marketplace remain outside Skillet’s product boundary unless that boundary is deliberately revisited.

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
