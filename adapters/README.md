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
skill is available without restarting the interactive session.

## Claude Code, Codex, and GitHub Copilot

Build the helper with `go install ./cmd/skillet-adapter`, then run:

```sh
adapters/claude-code/skillet-claude <candidate-id>
adapters/codex/skillet-codex <candidate-id>
adapters/github-copilot/skillet-copilot <candidate-id>
```

The default destinations are `~/.claude/skills`, `~/.codex/skills`, and
`~/.copilot/skills` respectively. Override them with
`SKILLET_CLAUDE_SKILLS_DIR`, `SKILLET_CODEX_SKILLS_DIR`, or
`SKILLET_COPILOT_SKILLS_DIR`.
The helper verifies the archive digest before replacing the selected skill.
These hosts discover skills at process/session boundaries, so the command
reports `reload_required: true`; start a new host session before relying on the
activated skill. GitHub Copilot CLI can instead refresh an existing interactive
session with `/skills reload`. No host directory is modified until this command
is run.
