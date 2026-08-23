# Harness adapters

These adapters keep host activation outside the Skillet registry while sharing
one MCP client and one digest-verifying materializer.

## Pi

Load `pi/skillet.ts` as a Pi extension and ensure `skillet-adapter` is on
`PATH` (or set `SKILLET_ADAPTER_BIN`). The extension provides:

```text
/skillet search <natural-language query>
/skillet activate <candidate-id>
/skillet deactivate <skill-name>
```

Activation is session-scoped. Pi reloads its resources immediately, so the
skill is available without restarting the interactive session. After a
successful reload Pi reports `activated`; after a successful deactivation
reload it reports `deactivated`. Telemetry is best-effort and does not control
whether the skill is active.

## Claude Code, Codex, GitHub Copilot, and OpenCode

Build the helper with `go install ./cmd/skillet-adapter`, then run:

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
These hosts discover skills at process/session boundaries, so the command
reports `reload_required: true`; start a new host session before relying on the
activated skill. GitHub Copilot CLI can instead refresh an existing interactive
session with `/skills reload`. No host directory is modified until this command
is run.

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
