# Skillet

![Skillet](assets/skillet.webp)

> Help agents find and use the right organisational capabilities and knowledge—without flooding context, losing provenance, or turning discovery into an execution authority.

AI agents are more useful when they can draw on specialised reusable guidance, organisation-specific knowledge, and the right supporting tools for a task. As those resources grow across central and repository-local sources, however, it becomes difficult to discover the right capability safely, difficult to keep knowledge and capability ranking semantics separate, and easy for work to depend on an unclear or changing revision.

Skillet is a single Go service for bounded agent capability discovery, organisational knowledge retrieval, reproducible skill materialisation, lightweight governance, deterministic declared composition, host-native distribution, collaboration, revision-bound improvement evidence, and opt-in capability-evolution evidence/control-plane primitives. It preserves the v1 skill registry/materialisation contract while adding verified vNext capability and knowledge domains, opt-in M2 enterprise controls, bounded M3 human/adoption surfaces, and bounded M4 evolution evidence behind the same product boundary.

The shipped v1 experience remains compatible:

- **Find:** search an organisation’s approved skill catalogue using natural-language intent.
- **Browse:** list the active approved skill metadata when an operator or agent needs to see what is available.
- **Choose:** review compact candidate descriptions; Skillet never silently selects a skill for the agent.
- **Use:** acquire one or more selected skills with an integrity-checked command and read their `SKILL.md` entrypoints when needed.
- **Reproduce:** lock exact Git commits and package digests, then restore those same packages on another run or machine.
- **Observe:** capable hosts may report `activated`, `deactivated`, `completed`, or `failed` against the exact materialized revision when they can truthfully observe those states.
- **Improve:** agents, clients, or users may attach bounded structured feedback to the exact materialized revision for later maintainer review.

The verified vNext surface additionally provides:

- **Scoped capability discovery:** search central and repository-local capabilities with organisation/namespace/repository isolation before ranking.
- **Capability kinds:** discover skills, playbooks, and metadata-only MCP tools in one capability index while keeping the legacy `search_skills` surface skill-only.
- **Progressive disclosure:** search compact descriptors first, then explicitly describe a selected capability; full skill packages and full MCP input schemas stay behind later boundaries.
- **Metadata-only tool discovery:** MCP tool catalogues are discoverable capabilities but do not become executable Skillet tools or gain delegated credentials.
- **Organisational knowledge:** index/search/read Markdown and OKF content with source revision/provenance/freshness metadata and explicit backlinks through a separate knowledge domain/index.
- **Governance:** surface active/deprecated/yanked state and replacement guidance without silent substitution; yanked capabilities are excluded from normal discovery while retained exact revisions remain restorable.
- **Review-only learning:** derive deterministic, evidence-traceable improvement candidates from revision-bound lifecycle/feedback observations without mutating source, ranking, governance, or active revisions.
- **Opt-in enterprise authorization:** normalize delegated scopes, workload roles, and configured trusted claims into a provider-neutral identity and enforce fine-grained organisation/namespace/repository/resource authorization without changing semantic relevance.
- **Microsoft Entra profile:** prove delegated-user and workload/service-principal paths through the same generic OIDC/JWKS and authorization model without a Microsoft Graph or Entra SDK runtime dependency.
- **Optional audit export:** retain local SQLite audit state as authoritative while allowing bounded best-effort export through a transport-neutral sink.
- **Server-rendered human UI:** browse catalogue, knowledge, collaboration, proposal, distribution, and bounded operator surfaces through the same application/authorization boundaries; Go templates, vendored htmx, and embedded assets avoid a second SPA authority.
- **Deterministic declared composition:** resolve source-declared required/recommended/conflicting relationships and curated collections to immutable lock plans; cycles, conflicts, unavailable or unauthorised dependencies fail closed and no capability is executed implicitly.
- **Host-native distribution:** project authorised immutable revisions into a deterministic Claude Code marketplace profile without creating a second publishing authority.
- **Bounded collaboration:** discussions, watches, moderation, and activity are Skillet-owned auxiliary state linked to stable identities; collaboration cannot silently change semantic ranking.
- **Reviewable improvement proposals:** bind derived evidence to immutable provenance, retain verification attempts, fail closed on stale bases, and never automatically edit canonical source or activate a revision.
- **Opt-in capability evolution evidence:** issue immutable proposal/base-bound experiments, retain append-only lineage and scoped champion/challenger fitness evidence, compare explicit improver provenance on compatible held-out distributions, propose versioned curriculum/evaluator updates, and exchange signed data-only evidence with external runners without making Skillet an execution engine or model trainer.
- **Deterministic release proof:** `go run ./cmd/skillet-verify` runs M1 Journeys A-E, protected v1/capability/knowledge retrieval metrics, M2 enterprise acceptance, M3 Journeys F-K, and integrated M4 Journeys L-Q with machine-readable evidence and the existing real-process headless-browser smoke.

Skills may optionally declare `metadata.version` using SemVer 2.0. Exact versions and ranges resolve once to one retained immutable revision; ranges choose the highest stable declared version. Prereleases require an explicit prerelease selector. Unversioned skills remain valid and Git tags are not version authority. M3 composition only follows explicit source-owned dependency metadata or curated collection manifests; it does not invent dependencies, execute them, or automatically upgrade an existing lock. Lockfile commit, tree, and archive SHA-256 fields remain authoritative.

Skillet is a registry/retrieval/distribution and bounded evidence/control-plane boundary, not an agent harness, execution sandbox, workflow engine, or model trainer. The repository ships a harness-neutral `skillet-client` for discovery, digest-verified materialization, lifecycle reporting, and structured feedback. Compatible clients and hosts may report lifecycle observations only when they can truthfully observe the corresponding state. M4 external runners execute outside Skillet and return signed, digest-bound evidence only.

The repository contains a runnable single-node vertical slice: strict configuration validation, SQLite WAL-backed catalogue state, configured Git/local polling, Agent Skills discovery/quarantine, deterministic package archives, content-addressed retention, Bleve-plus-vector retrieval primitives, scoped capability discovery, metadata-only MCP tool ingestion, Markdown/OKF knowledge retrieval, explicit backlinks, capability governance, configurable embeddings/listwise reranking adapters, signed package URLs, locked restoration, OIDC/JWKS validation, opt-in claims-based authorization, MCP search/materialisation, revision-bound lifecycle telemetry, structured feedback, reviewable improvement candidates/proposals, deterministic declared composition, a Claude Code distribution profile, server-rendered human/operator views, bounded collaboration, authoritative local audit events, optional audit export, M4 experiments/lineage/scoped fitness/provenance/meta-eval/curriculum/external-runner evidence, Prometheus counters, and deterministic M1+M2+M3+M4 release evidence.

## Local development

Go 1.25 and the pinned official MCP Go SDK are required.

```sh
go test ./...
go vet ./...
go run ./cmd/skillet -config skillet.example.yaml
```

For the complete deterministic vNext release proof (M1 + M2 + M3 + M4), run:

```sh
go run ./cmd/skillet-verify
```

The M3 gate drives a real local Skillet binary with headless Chrome/Chromium. Set `SKILLET_BROWSER_BIN` when the browser is not discoverable on `PATH` or in the common macOS/Windows install locations.

See [`docs/m1-release-gate.md`](docs/m1-release-gate.md) for the Journey A-E contract, [`docs/enterprise-operations.md`](docs/enterprise-operations.md) for the M2 enterprise controls, [`docs/m3-release-gate.md`](docs/m3-release-gate.md) for M3 Journeys F-K, and [`docs/m4-release-gate.md`](docs/m4-release-gate.md) for M4 Journeys L-Q, enablement, trust boundaries, and acceptance evidence.

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
system prompt.

The host instruction establishes **when Skillet discovery is required**. The
`find-skills` skill defines **how that discovery is performed**: it calls
`list_skills` or `search_skills`, treats candidate metadata as untrusted data,
and preserves Skillet's explicit selection and materialisation boundary.

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

Because hosts may otherwise begin solving a task directly or select a more
specific local skill before consulting Skillet, configure the host's
user-level instructions with an explicit routing gate. For example:

```text
Skillet discovery is a mandatory routing gate for non-routine work.

Before doing substantive work on any specialised, domain-specific,
repository-level, architecture, QA, delivery, or workflow request, consult
Skillet using the installed find-skills workflow.

For a covered request, the first task-routing action MUST be Skillet discovery.
Do not begin repository exploration, invoke repository-analysis tools, select
another task-specific skill, or start solving the task directly before this
discovery step.

Use search_skills for task-intent discovery. Use list_skills only when catalogue
browsing is needed. Prefer a relevant approved Skillet skill when one is
returned.

This routing gate applies even when the task appears directly solvable, another
repository-analysis tool is available, the agent already knows how to perform
the task, or no other task-specific skill has yet been selected.

Do not query Skillet for routine tasks already directly supported and unlikely
to benefit from specialised guidance.

If Skillet or the find-skills workflow is unavailable, state that explicitly
and continue using the best available approach.
```

After Skillet routing, hosts should follow any applicable task-specific or
tool-specific guidance.

The MCP tool descriptions still enforce the important server boundary: search
returns metadata only, and Skillet never silently selects skills, writes to
host directories, or executes skill scripts. Hosts that do not support Agent
Skills can still use the MCP tools directly or provide their own thin adapter.

The example starts in explicit development mode with no external database or model provider. Endpoints are `/healthz`, `/readyz`, `/metrics`, and `/mcp`. Production deployments must configure OIDC or static bearer authentication and signing keys.

## Product boundary

The service is intentionally a single-node Go modular monolith backed by SQLite for its current verified surface. Operators configure approved sources. Capability scope constrains eligibility before ranking; capability and knowledge models/indexes remain separate. Admitted skill packages retain exact commits/trees and SHA-256 digests, while metadata-only tools carry no execution authority.

The core service will not execute skill scripts or discovered tools, orchestrate workflows, infer authoritative semantic graph relationships, generate final RAG answers, write to client harness directories, train models, or execute external-runner workloads. Deterministic composition is limited to explicit source-owned relationships/collections and immutable plan/lock generation; M4 external runners receive data-only bounded dispatches and execute outside Skillet. The generic client performs explicit, user-invoked skill materialization after verifying the returned archive digest.

A compatible MCP host must already provide shell/download capability, outbound HTTPS, permission to write to a user cache outside the repository, and permission to read the extracted `SKILL.md`. Hosts without those capabilities may be search-only. Skillet is not marketed as universally compatible with every MCP client.

The remote materialisation flow returns a signed immutable package URL, a fixed POSIX or PowerShell acquisition command, an external cache destination, a deterministic `skillet-lock.json` entry, and a lifecycle reference bound to the exact revision/package/materialisation. The server never writes to the MCP client filesystem and never executes skill-provided scripts.

Lifecycle observations and structured feedback are optional evidence. Derived improvement candidates, M3 improvement proposals, and M4 experiment/lineage/fitness/provenance/curriculum/runner records are review or evidence artefacts. None of these authorize execution, change active host state, alter retrieval ranking, deprecate a revision, edit `SKILL.md`, mutate protected evaluators in place, or apply/promote source changes automatically.

See [the implementation handoff](docs/implementation-handoff.md), [M1 release gate](docs/m1-release-gate.md), [enterprise operations](docs/enterprise-operations.md), [M3 release gate](docs/m3-release-gate.md), [M4 release gate](docs/m4-release-gate.md), and [architecture decisions](docs/adr/) for the verified boundaries and deferred scope.

## Generic client

MCP provides the harness-neutral discovery and immutable package contract. The
generic client performs the last-mile download, digest verification, extraction,
and installation into an explicit destination. It does not assume or identify a
specific agent harness and does not claim that the host has reloaded the skill.

Build it with:

```sh
go install ./cmd/skillet-client
```

Tagged releases publish `skillet-client` binaries alongside the Skillet server
for Linux, macOS, and Windows on amd64 and arm64.

Search and materialize directly into any compatible skill directory:

```sh
skillet-client search -query "find a skill for deterministic tool batching"
skillet-client materialize -candidate <candidate-id> -destination "$HOME/.codex/skills"
```

The destination controls where the extracted `SKILL.md` is placed; it is the
only host-specific input required for materialization. Set `SKILLET_MCP_URL` or
`SKILLET_TOKEN` when the defaults are not suitable.

## Host integration boundary

MCP provides discovery and immutable package transport. It does not itself
activate a `SKILL.md` in every host, because skill loading is host-specific.
After a materialization, use the host's normal session or resource reload
mechanism before relying on the new skill. Report lifecycle events only after
the host has actually observed them:

```sh
skillet-client lifecycle \
  -server "$SKILLET_MCP_URL" \
  -reference "$LIFECYCLE_JSON" \
  -event activated \
  -source my-host \
  -correlation "$SESSION_ID"
```

Do not report `activated` just because materialization succeeded. Likewise,
report `completed`, `deactivated`, or `failed` only when the client or host
actually observes that outcome.

Clients may submit bounded structured feedback against the same immutable
materialization reference:

```sh
skillet-client feedback \
  -server "$SKILLET_MCP_URL" \
  -reference "$LIFECYCLE_JSON" \
  -category ambiguous_instruction \
  -summary "The rollback step did not identify which generated file to remove."
```

Supported categories are `step_failed`, `workaround_required`, `user_correction`, `ambiguous_instruction`, `compatibility_mismatch`, `improvement_suggested`, and `effective_pattern`. Use `effective_pattern` only for a concrete reusable behaviour that materially helped the task; ordinary success, activation, or generic praise is not enough. Summaries are capped at 1,000 characters. Maintainers can query revision-bound observations with `skillet-client feedback-list`; listing must be scoped to a skill or immutable revision.

## How it works

1. An operator configures approved capability/skill sources and, where required, knowledge or MCP-tool metadata sources.
2. Skillet discovers and validates skills, retains immutable packages, imports compact metadata-only tool descriptors, and builds the separate capability index.
3. Knowledge ingestion builds a separate Markdown/OKF catalogue/index with provenance and explicit links/backlinks.
4. An agent or human uses the appropriate bounded discovery surface: legacy `search_skills` for v1 compatibility, scoped `search_capabilities` for capability discovery, `search_knowledge` for information needs, or the server-rendered human catalogue.
5. A caller explicitly describes/selects a capability. Skills may then be materialised; discovered MCP tools are not executed by Skillet.
6. When source-owned composition is requested, Skillet deterministically resolves declared relationships/collections to an immutable preview/lock plan; this does not activate or execute capabilities.
7. Approved immutable revisions may be projected into supported host-native distribution profiles such as the Claude Code marketplace snapshot.
8. Skill materialisation returns a short-lived package URL, integrity digest, fixed acquisition command, lockfile entry, and lifecycle reference tied to the exact materialisation.
9. A capable host may report truthful lifecycle observations and bounded feedback against that immutable reference.
10. Maintainers may inspect deterministic improvement candidates and prepare provenance-bound reviewable proposals; source/catalogue/ranking/governance remain unchanged until an explicit human-reviewed change is made through the owning source workflow.
11. When explicitly enabled and authorized, M4 can issue immutable experiments from reviewed proposals, retain lineage/scoped fitness/provenance/curriculum evidence, and exchange signed data-only dispatch/result records with external runners; those records do not automatically mutate source, ranking, governance, protected evaluators, or canonical promotion state.

Skillet never silently selects or installs a skill, puts full package contents or full tool schemas into ordinary search results, executes discovered tools/scripts or external-runner workloads, cross-ranks organisational knowledge with capabilities, writes to the client filesystem, changes ranking from telemetry/collaboration/M4 evidence, invents undeclared dependencies, or automatically rewrites/promotes source from feedback/proposals/experiments.

## Trust and security boundary

Skillet is a distribution and retrieval service for sources that an organisation has already approved. Source admission is configuration-controlled for the current product; there is no public submission or public marketplace workflow.

The service protects the distribution path with exact commits/trees, specification validation, safe deterministic packaging, SHA-256 integrity, durable historical retention, organisation/scoped eligibility, authentication, optional fine-grained claims authorization, short-lived package URLs, governance state and audit events. Source repositories, knowledge documents, tool metadata, package files, lifecycle reports, feedback summaries, collaboration text, proposal evidence, curriculum feedback, and external-runner payloads remain untrusted data and are never treated as instructions by the retrieval pipeline.

Skillet does not claim to certify that a skill is safe to execute or that a discovered external tool should be invoked. It does not execute scripts/tools or runner workloads, enforce `allowed-tools`, sandbox a consuming agent, train models, or replace an organisation's source-repository review, CI controls, marketplace approval, endpoint protection, runtime authorization, or external-runner isolation. Those controls belong upstream and in the consuming host/runner infrastructure. In particular, a prompt that asks an agent to request confirmation is not an enforcement boundary; real execution authorization must be implemented by the host or infrastructure.

## Retrieval evaluation

The repository includes deterministic protected routing/evaluation corpora under [`evals/`](evals/). They contain repository-owned fixture metadata/content and deterministic synthetic vectors only; the external `mhingston/agent-skills` repository is intentionally not vendored.

The authoritative aggregate command is:

```sh
go run ./cmd/skillet-verify
```

It protects v1 skill-routing baseline metrics, scoped capability top-1/recall/multi-recall/per-kind recall/negative activation/scope leakage/kind confusion, knowledge retrieval metrics, the integrated M1 Journeys A-E acceptance result, M2 enterprise identity/authorization acceptance, M3 Journeys F-K human/adoption acceptance, and integrated M4 Journeys L-Q capability-evolution acceptance. `artifacts/verification/m4-acceptance.json` records default-off isolation, experiment/provenance/lineage/fitness/meta-eval/curriculum/runner security evidence and preserved M1-M3 status alongside the existing M3 evidence.

The real demonstration-corpus admission/search test is opt-in and never
vendors the corpus:

```sh
SKILLET_EXTERNAL_CORPUS=1 go test ./internal/e2e -run TestExternalAgentSkillsCorpus
```

The remote MCP journey, including command execution and locked restoration,
is covered by `TestRemoteMCPSearchMaterializeExecuteAndRestore`.

## Future work

The verified M1 + M2 + M3 + M4 surface intentionally stops before execution brokerage inside Skillet, workflow orchestration, automatic proposal application/promotion, autonomous model training, generated-answer workflows, public submission/marketplace authority, and distributed operation. Relevant future work includes:

- **Richer governance:** human admission/review workflows, stronger publisher identity, catalogue audits, stale-description/overlap analysis, richer revocation policy and organisation-specific approval integrations.
- **Retrieval quality/scale:** larger evaluated corpora, diversity-aware ranking, query rewriting, learned routing where justified, larger-scale vector indexes, and human relevance judgements while preserving capability/knowledge domain separation.
- **Knowledge/source integrations:** source-specific Confluence/Jira/Snowflake-style harvesters, richer freshness policy, and graph-assisted discovery only where provenance/authority semantics are explicit.
- **Distribution/provenance:** package signatures or attestations, object-storage-backed packages, federation, additional reviewed host profiles, and stronger retained-version policies.
- **Security integrations:** optional integration with an organisation’s existing repository/marketplace scanners, policy engines, SARIF pipelines, and approval records. Skillet should consume trusted decisions rather than become a general-purpose malware scanner or execution sandbox.
- **Evidence/proposal workflows:** richer external review/export/integration for prepared improvement proposals and M4 evidence while keeping application/merge/promotion and ranking/governance changes explicit and human reviewed.
- **Client integrations:** native host materialisation, automatic resource-link downloads, richer host lifecycle integration, and lockfile maintenance by capable MCP hosts.
- **Operations and scale:** PostgreSQL/object storage adapters, distributed search, horizontal deployment, backup/restore tooling, richer dashboards, and additional MCP/browser compatibility testing.

These items should be driven by pilot evidence. Dynamic tool execution/brokering inside Skillet, automatic capability execution/activation, public submissions, workflow orchestration, automatic source mutation/application/promotion, generated final answers, autonomous training, and a general-purpose security marketplace remain outside Skillet’s current product boundary unless that boundary is deliberately revisited. M4's external-runner protocol is evidence exchange across that boundary, not execution brokerage.

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
