# Enterprise identity, authorization, and audit operations

M2 enterprise controls are additive and opt-in. The existing M1 release gate, retrieval behavior, and compatibility surfaces remain part of `go run ./cmd/skillet-verify`.

Skillet keeps four boundaries separate:

1. OIDC/JWKS authentication verifies token signature, issuer, audience, lifetime, and trusted claim shapes.
2. The auth boundary normalizes trusted provider claims into `auth.Identity` permissions and configured attributes.
3. `authorization.mode: claims` maps those normalized values to Skillet actions and authoritative organisation/namespace/repository/resource scope.
4. Local SQLite audit state remains authoritative; optional export happens only after local commit and is best-effort.

Request filters are selectors, not authority. Authorization metadata is never added to semantic retrieval text and must not change the ranking evidence of results that remain visible.

## Minimal non-enterprise configuration

For an existing single-organisation deployment that does not need claims-based enterprise authorization, keep the current auth mode and compatibility authorization. Omitting the `authorization` block is equivalent to `mode: compatibility`.

```yaml
organization:
  id: demo

auth:
  mode: static
  static_token_env: SKILLET_STATIC_TOKEN

authorization:
  mode: compatibility
```

Set the token in the environment before startup:

```sh
export SKILLET_STATIC_TOKEN='replace-with-a-secret'
```

Compatibility mode preserves the documented pre-M2 behavior: a trusted authenticated identity is restricted to its exact organisation, without inventing namespace/repository entitlements. Development mode also retains its existing behavior and does not synthesize a trusted user identity.

Claims authorization is intentionally unavailable with `static` or `development` auth. `authorization.mode: claims` requires `auth.mode: oidc`.

## Generic OIDC claims authorization

The generic enterprise configuration is provider-neutral. Provider-specific claim names stop at the trusted authentication boundary.

```yaml
organization:
  id: acme

auth:
  mode: oidc
  issuer: https://identity.example.com/acme
  audience: skillet-api
  organization_claim: tenant_id
  scope_claim: scope
  role_claim: roles
  attribute_claims:
    groups: group_ids

authorization:
  mode: claims
  grants:
    - permissions: [skills.read]
      actions:
        - capability.search
        - capability.describe
      resources:
        - namespace: engineering
          repository: checkout-api

    - permissions: [skills.materialize]
      attributes:
        groups: [platform]
      actions:
        - capability.materialize
      resources:
        - namespace: engineering
          repository: checkout-api

    - permissions: [knowledge.read]
      actions:
        - knowledge.search
        - knowledge.read
      resources:
        - namespace: engineering
```

Supported actions are currently:

- `capability.search`
- `capability.describe`
- `capability.materialize`
- `knowledge.search`
- `knowledge.read`
- `evidence.report`
- `evidence.review`
- `governance.read`

Within one grant, configured permission and attribute selectors are ANDed; separate grants are ORed. Resource rules are exact boundaries. A repository rule requires its namespace. Stable resource IDs can further narrow a rule.

Missing optional permission/attribute claims grant nothing. Present malformed configured claims fail authentication closed rather than being ignored.

## Delegated users and workload identities

Delegated OAuth scopes and workload/application roles are normalized into the same provider-neutral permission set. Authorization therefore does not need separate policy implementations for people and service principals.

A delegated token commonly supplies permissions through the configured scope claim:

```json
{
  "sub": "user-123",
  "tenant_id": "acme",
  "scope": "skills.read knowledge.read"
}
```

A workload token commonly supplies permissions through the configured role claim:

```json
{
  "sub": "service-456",
  "tenant_id": "acme",
  "roles": ["skills.read", "skills.materialize"]
}
```

Both are subject to the same issuer, audience, signature, organisation, action, and resource checks.

## Microsoft Entra deployment profile

Microsoft Entra is a deployment profile over the generic OIDC/JWKS boundary, not a separate authorization implementation. Use a tenant-specific v2 issuer and map:

- `tid` -> organisation identity;
- `scp` -> delegated permissions;
- `roles` -> workload/application permissions.

Example:

```yaml
organization:
  id: "11111111-1111-1111-1111-111111111111"

auth:
  mode: oidc
  issuer: "https://login.microsoftonline.com/11111111-1111-1111-1111-111111111111/v2.0"
  audience: "api://<skillet-api-application-id>"
  organization_claim: tid
  scope_claim: scp
  role_claim: roles

authorization:
  mode: claims
  grants:
    - permissions: ["Skillet.Read"]
      actions: ["capability.search", "capability.describe"]
      resources:
        - namespace: engineering
          repository: api

    - permissions: ["Skillet.Read.All"]
      actions: ["capability.search", "capability.describe"]
      resources:
        - namespace: engineering
          repository: api
```

See `docs/microsoft-entra-oidc.md` for the full profile, including deterministic offline delegated/workload proof and group-overage behavior.

## Audit export

Local SQLite `audit_events` remains authoritative. Export is disabled by default. The reference JSONL sink can be enabled with:

```sh
export SKILLET_AUDIT_EXPORT_SINK=jsonl
export SKILLET_AUDIT_EXPORT_TARGET=stdout
```

`SKILLET_AUDIT_EXPORT_TARGET` may instead be an append-only file path. Export receives only the bounded normalized envelope after the local audit/state transaction has committed. Export failure or panic is degradation only: it is counted/logged and cannot roll back authoritative state.

Do not treat successful external delivery as the source of truth for whether a Skillet operation committed. See `docs/audit-export.md` for the envelope and failure contract.

## Troubleshooting

### Startup rejects claims authorization

Check that `auth.mode` is `oidc`, at least one authorization grant exists, and every configured attribute selector is mapped by `auth.attribute_claims`. Claims mode deliberately fails configuration rather than silently reverting to compatibility.

### Every token is unauthorized

Check exact issuer and audience values first. Then verify the configured organisation claim exists and normalizes to the same value as `organization.id`. For Entra, use a tenant-specific issuer and ensure `tid` matches that organisation ID.

### Delegated users work but service principals do not

Confirm the provider emits application roles and that `auth.role_claim` points at them. For Entra this is `roles`; delegated scopes are normally in `scp`. Both must map to permission names referenced by an authorization grant.

### A token authenticates but cannot see a repository

Authentication and authorization are separate. Verify the normalized permission/attribute values, requested action, and the resource's authoritative namespace/repository provenance. Changing an MCP/HTTP repository filter cannot grant access.

### A configured claim is malformed

Malformed configured scope, role, or trusted-attribute claims fail authentication closed. Fix the token/identity-provider mapping; do not coerce malformed values downstream.

### Audit export is failing

Treat the sink as degraded, not authoritative. Confirm the local operation and local `audit_events` state first, then inspect sink configuration/connectivity. Core does not retry failed export deliveries.

### Ranking appears to change after enabling claims authorization

That is a regression. Claims authorization filters disclosure after protected retrieval/ranking and must preserve the surviving candidate's semantic rank/score evidence. Run the M2 acceptance gate below before changing ranking code or baselines.

## Release proof and evidence

Run the complete release gate with:

```sh
go run ./cmd/skillet-verify
```

The command preserves all M1 Journey A-E checks, v1 compatibility, retrieval baselines, `go vet`, and race checks, and adds deterministic M2 enterprise proof for:

- delegated user and workload identity authorization;
- allow/deny scope behavior;
- zero cross-organisation leakage;
- capability/knowledge/materialization claims enforcement;
- missing/malformed/untrusted claim denial;
- static/development auth compatibility;
- no authorization-induced protected ranking change;
- audit-export failure preserving authoritative state.

Machine-readable output is written under `artifacts/verification/`:

- `verification.json` — the existing release report with an additive M2 journey and a pointer to enterprise evidence;
- `enterprise-acceptance.json` — explicit M2 assertions and their source proof steps;
- the existing retrieval/capability/knowledge evaluation reports.

CI retains the whole directory as the verification artifact.

## Explicitly deferred scope

M2 does **not** add:

- SCIM provisioning or lifecycle management;
- Microsoft Graph group-overage expansion or directory lookup;
- provider-specific SIEM/export SDK integrations.

Those require separate availability, trust, caching, credential, privacy, and failure contracts. The current extension points are generic OIDC claim mapping, claims authorization, and the transport-neutral audit-export sink.
