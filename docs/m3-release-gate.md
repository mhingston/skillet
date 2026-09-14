# M3 release gate

M3 is release-gated by the same authoritative command as M1 and M2:

```sh
go run ./cmd/skillet-verify
```

The command is intentionally aggregate rather than a replacement for the focused package and E2E tests. It composes the already-shipped M3 browser, composition, distribution, collaboration, evidence/proposal, and operator proofs with the protected M1 retrieval baselines and M2 enterprise acceptance report. Missing proof fails closed.

## Journeys

The M3 machine-readable acceptance report records six release journeys:

- **F — discovery and human catalogue:** authenticated catalogue/knowledge browsing, scoped visibility, lifecycle rendering, inert untrusted content, responsive/keyboard behaviour, pinned local assets, and a real local Skillet binary driven by headless Chrome/Chromium.
- **G — composition and lock preview:** declared relationships, deterministic transitive resolution, compatible-version selection, cycles, conflicts, unavailable/yanked dependencies, collections, immutable lock provenance, and fail-closed unauthorised dependencies.
- **H — host-native distribution:** deterministic Claude Code marketplace projection, scope/lifecycle filtering, immutable Git provenance, reviewed compatibility fixture, and source-update behaviour.
- **I — collaboration and learning signals:** authorised discussions/watch/moderation/activity, inert user text, audit evidence, deterministic lifecycle/feedback-derived improvement candidates, and zero semantic-ranking effect from collaboration state.
- **J — reviewable improvement proposal:** immutable provenance binding, secret-redacted bounded handoff, failed/passing verification history, stale-base refusal, and zero automatic canonical/ranking mutation.
- **K — operator/admin security:** trusted-identity and cross-organisation isolation, bounded read-only state, explicit operator permissions, identity-bound CSRF, and authoritative local audit retention when export fails.

The browser process smoke uses the installed Chrome/Chromium executable directly from Go, so the production/test dependency remains the shipped Go binary plus a browser rather than Node or a browser automation framework. Set `SKILLET_BROWSER_BIN` when Chrome/Chromium is not discoverable on `PATH` or in the common macOS/Windows locations.

## Evidence contract

`artifacts/verification/m3-acceptance.json` is the M3 acceptance record. It includes:

- pass/fail for Journeys F-K;
- headless-browser driver status and harness identity;
- cross-scope/cross-organisation leakage evidence with an expected value of zero;
- deterministic dependency resolution plus cycle/conflict/unauthorised-dependency proof;
- deterministic distribution and host-profile validation;
- collaboration ranking effect with an expected value of zero;
- proposal provenance, verification, and automatic canonical mutation evidence (expected zero mutations);
- operator authorization/CSRF/audit proof;
- preserved M1 integrated acceptance and protected metric results;
- preserved M2 enterprise acceptance status;
- `go test`, `go vet`, and `go test -race` status.

`artifacts/verification/verification.json` remains the aggregate report and links to the M1 retrieval reports, `enterprise-acceptance.json`, and `m3-acceptance.json`. CI prints all three acceptance/aggregate reports in the job summary and retains the whole `artifacts/verification/` directory as a workflow artifact.

## Failure policy

M3 does not weaken earlier release contracts. The aggregate command exits non-zero when any protected M1 metric regresses, M2 enterprise acceptance fails, a required M3 journey/assertion is absent or fails, the real-process browser smoke fails, or Go test/vet/race checks fail.

Focused failures should be reproduced with the exact command recorded in `verification.json`. The M3 report deliberately references named verification steps instead of duplicating domain logic in the reporting layer.

## Boundaries preserved by the gate

M3 adds human/adoption surfaces without changing source of truth. Browser/collaboration/proposal state cannot become semantic-ranking input, dependency resolution cannot invent undeclared relationships or execute capabilities, distribution remains a projection of already-authorised immutable revisions, and proposals remain review-only artefacts rather than an automatic source mutation path.

Provider-specific identity remains outside the M3 domain. The gate preserves the M2 provider-neutral trusted identity/authorization model and separately proves the Entra-compatible delegated/workload path through `enterprise-acceptance.json`.
