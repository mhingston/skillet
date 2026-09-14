# Bounded operator UI

The M3 operator workspace is available at `/ui/operator`. It is a browser adapter over existing Skillet operational state and the M2 authorization boundary; it is not a second configuration or governance authority.

## Authorization

Claims deployments should grant the operator actions explicitly:

```yaml
authorization:
  mode: claims
  grants:
    - permissions: [skillet.operator.read]
      actions: [operator.read]
      resources:
        - {}

    - permissions: [skillet.operator.audit]
      actions: [operator.audit_probe]
      resources:
        - {}
```

`operator.read` permits the organisation-scoped operational view. `operator.audit_probe` permits the bounded audit-export probe. A user with ordinary catalogue/knowledge permissions receives `403 Forbidden` from both the operator page and operator mutation endpoint.

The organisation is taken from the trusted authenticated identity. The UI does not accept an organisation selector from query/form input, so a caller cannot widen scope by changing a URL.

## What the workspace shows

The view exposes bounded operational projections only:

- registered repositories and their source URL/ref/trust/owner as read-only values;
- active and quarantined revision counts plus the last recorded repository reconciliation outcome;
- quarantined immutable revisions and bounded validation findings;
- structured feedback and lifecycle evidence counts;
- recent audit event envelope fields, excluding arbitrary `details_json`;
- runtime readiness/reconciliation counters already owned by the server;
- whether capability/knowledge services and claims authorization are configured;
- audit exporter enabled/attempt/failure counters when the configured exporter exposes them.

Raw bearer tokens, refresh tokens, signing keys, provider claim maps, client secrets, audit detail payloads, and other credential/configuration secrets are never rendered.

## Source-of-truth boundary

The following remain read-only in the operator UI:

- `SKILL.md` capability definition and metadata;
- source-authored governance state, owner/maintainers, reason, deprecation, and replacement guidance;
- repository URL/ref/trust/owner startup configuration;
- Git/OKF knowledge content and source metadata;
- startup YAML and environment configuration.

Change those values in their owning source/configuration and allow normal reconciliation to produce a new immutable projection. The operator workspace intentionally has no generic database editor, YAML writer, or browser-only yank/deprecate path.

## Audit-export probe

The only M3.3 browser mutation is an explicitly bounded audit-export probe. It:

1. authorizes `operator.audit_probe` through the same M2 `AuthorizationPolicy` contract;
2. requires an unguessable server-generated CSRF token bound to the trusted organisation + subject;
3. writes one local `operator_audit_probe` event through the existing catalogue audit operation, including actor and request correlation metadata;
4. offers the bounded event envelope to the already configured best-effort audit exporter.

Exporter failure cannot roll back the local audit record. The probe does not change source, governance, retrieval ranking, package history, configuration, or execution state.

The endpoint always redirects to a fixed internal path after success; there is no caller-controlled return URL.
