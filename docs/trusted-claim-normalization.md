# Trusted claim normalization

Skillet keeps provider-specific JWT claim names at the authentication boundary. Downstream authorization receives a provider-neutral `auth.Identity` containing a subject, organisation, normalized permissions, and explicitly configured attributes. Raw verified claim payloads are not retained on the identity.

## Default behaviour

No extra claim mapping is required for common OIDC access tokens:

- delegated permissions are read from `scope`, falling back to `scp` when `scope` is absent;
- workload/application permissions are read from `roles`;
- delegated scopes and workload roles are deduplicated into one `Permissions` set;
- `Scopes` remains a compatibility projection of delegated scopes only;
- no group or entitlement attribute is trusted unless it is explicitly configured.

This preserves the existing `scope` / `scp` behaviour while allowing a non-interactive workload token to present the same provider-neutral permission names through `roles`.

## Optional claim mapping

Only configure mappings when a provider uses different verified claim names:

```yaml
auth:
  mode: oidc
  issuer: https://issuer.example
  audience: skillet
  organization_claim: tenant
  scope_claim: permissions
  role_claim: application_roles
  attribute_claims:
    groups: group_ids
    entitlements: licensed_features
```

`attribute_claims` maps a provider-neutral attribute name on `auth.Identity` to one verified JWT claim name. The application and authorization packages consume the normalized key (`groups`, `entitlements`, or another configured name), never the provider claim name.

Claim mappings are rejected outside OIDC mode. Claim names and normalized attribute names must be non-empty tokens without surrounding or embedded whitespace. Duplicate configured claim sources are rejected.

## Parsing and fail-closed rules

Delegated scope strings use standard whitespace-delimited OAuth scope syntax. Scope arrays are also accepted when every element is an exact non-empty string. Duplicate scopes are harmless and collapse to one permission.

Role and configured attribute claims accept either one exact non-empty string or an array of exact non-empty strings. Duplicate values collapse deterministically; attribute arrays are returned in sorted order. Missing optional role or attribute claims grant nothing.

A present configured claim with the wrong JSON type, an empty string, surrounding whitespace, a non-string array member, or an empty array member makes authentication fail closed. Malformed trusted claims are never ignored in a way that could broaden authorization.

## Boundary

`internal/auth` is the only package that interprets JWT claim names. `internal/authorization` and application/domain packages must use `Identity.Permissions` and `Identity.Attributes` rather than checking `scope`, `scp`, `roles`, provider group claims, or other raw JWT fields.
