# Optional host integrations

These integrations keep host activation outside the Skillet registry while
sharing the harness-neutral `skillet-client` and its digest-verifying
materializer. They are optional; any MCP-capable host can use the generic
client directly.

## Generic client

Build the client with:

```sh
go install ./cmd/skillet-client
```

Use an explicit destination instead of a harness-specific wrapper:

```sh
skillet-client search -query "find a skill for deterministic tool batching"
skillet-client materialize -candidate <candidate-id> -destination "$HOME/.codex/skills"
```

The client verifies the package digest, extracts the selected skill, and prints
the installed entrypoint plus its immutable lifecycle reference. It does not
activate or reload a host. `skillet-adapter` remains a compatibility alias.

## Pi

Load `pi/skillet.ts` as a Pi extension and ensure `skillet-client` is on `PATH`
(or set `SKILLET_CLIENT_BIN`; the old `SKILLET_ADAPTER_BIN` name remains
accepted). The extension provides:

```text
/skillet search <natural-language query>
/skillet activate <candidate-id>
/skillet deactivate <skill-name>
/skillet feedback <skill-name> <category> <summary>
```

Activation is session-scoped. Pi reloads its resources immediately, so the
skill is available without restarting the interactive session. After a
successful reload Pi reports `activated`; after a successful deactivation
reload it reports `deactivated`. Telemetry is best-effort and does not control
whether the skill is active.

Feedback is explicit rather than inferred from the conversation. Pi resolves
the named active skill to the exact materialization reference already held for
the session and submits only the selected category plus a short summary.

## Claude Code, Codex, GitHub Copilot, and OpenCode

Build the generic client with `go install ./cmd/skillet-client`, then run:

```sh
adapters/claude-code/skillet-claude <candidate-id>
adapters/codex/skillet-codex <candidate-id>
adapters/github-copilot/skillet-copilot <candidate-id>
adapters/opencode/skillet-opencode <candidate-id>
```

The default destinations are `~/.claude/skills`, `~/.codex/skills`,
`~/.copilot/skills`, and `~/.config/opencode/skills` respectively. Override
them with `SKILLET_CLAUDE_SKILLS_DIR`, `SKILLET_CODEX_SKILLS_DIR`,
`SKILLET_COPILOT_SKILLS_DIR`, or `SKILLET_OPENCODE_SKILLS_DIR`.
The helper verifies the archive digest before replacing the selected skill.
These hosts discover skills at process/session boundaries; start a new host
session before relying on the materialized skill. GitHub Copilot CLI can instead
refresh an existing interactive session with `/skills reload`. No host
directory is modified until this command is run.

Materialization results include a `lifecycle` reference bound to the immutable
revision and package digest. All wrappers expose the same optional reporting
surface for integrations that can reliably observe host lifecycle events:

```sh
adapters/claude-code/skillet-claude lifecycle -reference "$LIFECYCLE_JSON" -event activated -correlation "$SESSION_ID"
adapters/codex/skillet-codex lifecycle -reference "$LIFECYCLE_JSON" -event deactivated -correlation "$SESSION_ID"
```

The Copilot and OpenCode wrappers support the same `lifecycle` form. Do not
report `activated` merely because materialization succeeded: report it only
after the host has actually loaded the skill. Likewise, report `completed` or
`failed` only when the host can observe that outcome. Lifecycle telemetry is
optional evidence; it never authorizes execution or changes retrieval ranking.

All four wrappers also expose explicit structured feedback using the same
materialization reference:

```sh
adapters/claude-code/skillet-claude feedback \
  -reference "$LIFECYCLE_JSON" \
  -category ambiguous_instruction \
  -summary "The rollback step did not identify which generated file to remove."
```

Supported feedback categories are `step_failed`, `workaround_required`,
`user_correction`, `ambiguous_instruction`, `compatibility_mismatch`,
`improvement_suggested`, and `effective_pattern`. Use `effective_pattern` only
for a concrete reusable behaviour that materially helped the task; ordinary
success, activation, or generic praise is not enough. Summaries are capped at
1,000 characters. Do not send session transcripts, credentials, user identity,
or instructions to execute. Feedback is untrusted evidence for maintainer
review; recording it never edits a skill, opens an issue, changes ranking, or
deprecates a revision.

Maintainers can query feedback directly with the shared helper:

```sh
skillet-client feedback-list -server "$SKILLET_MCP_URL" -skill-id org/repo/skill -limit 25
```

Feedback listing must be scoped to a skill or immutable revision.
