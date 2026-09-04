# ADR-009: lifecycle telemetry is revision-bound evidence

## Status

Accepted.

## Context

Skillet can prove which immutable revision was discovered and materialized, but
materialization does not prove that a consuming harness loaded or successfully
used the skill. Treating those states as equivalent would produce misleading
catalogue telemetry and weak relationship signals.

## Decision

`materialize_skill` returns a `lifecycle` reference containing the immutable
revision ID, stable skill ID, commit, tree, and exact package digest. A capable
host may pass that reference to `report_skill_lifecycle` with one of four small
events: `activated`, `deactivated`, `completed`, or `failed`.

Skillet validates the full reference against immutable catalogue history before
recording the observation. Observations are append-only audit evidence, may
carry an opaque run/session correlation ID and harness source, and do not carry
individual productivity identity by default.

Lifecycle reporting is optional and best-effort. It does not authorize skill
execution, alter active host state, trigger another skill, or affect retrieval
ranking. `completed` and `failed` must only be reported by integrations that can
actually observe those outcomes.

The generic client exposes the same reporting capability for clients that can
truthfully observe a host boundary. It does not infer activation from
materialization: skill discovery may happen at a later process, session, or
explicit reload boundary.

## Consequences

This creates trustworthy revision-bound evidence for later aggregate analysis,
including search-to-activation conversion, stale-skill detection, co-use, and
ordered transitions. Those derived signals remain a separate follow-on and
must be evaluated before influencing search ranking.
