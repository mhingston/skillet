# Microsoft Entra OIDC deployment profile

Microsoft Entra ID is supported as a deployment profile over Skillet's generic OIDC/JWKS authentication and claims authorization boundaries. This profile does not add Microsoft-specific checks to domain code, require a Microsoft authentication SDK, or make Microsoft Graph a runtime dependency.

The committed profile is intentionally narrow:

- a tenant-specific OIDC issuer and its discovered JWKS endpoint authenticate tokens;
- `tid` is mapped to Skillet's normalized organisation identity;
- delegated access uses the Entra `scp` claim;
- workload/service-principal access uses the Entra `roles` claim;
- both claims become provider-neutral `Identity.Permissions` before authorization;
- `authorization.mode: claims` applies the same action and resource policy to either identity type.

Microsoft's access-token claim reference documents `scp` for delegated user permissions, `roles` for application permissions, and `tid` as the tenant identifier: <https://learn.microsoft.com/en-us/entra/identity-platform/access-token-claims-reference>.

## Recommended single-tenant configuration

Register Skillet as an API/resource in Entra. Expose the delegated scopes and application roles that represent the Skillet actions you intend to grant. The exact names are deployment policy; the example below uses `Skillet.Read` for a delegated scope and `Skillet.Read.All` for an application role.

Use the tenant-specific v2 issuer rather than a `common`, `organizations`, or `consumers` issuer. Configure `audience` to the exact `aud` value Entra emits for the Skillet API in your registration.

```yaml
organization:
  # Keep this equal to the tenant value trusted from the token's tid claim.
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
      actions: ["capability.search"]
      resources:
        - namespace: engineering
          repository: api

    - permissions: ["Skillet.Read.All"]
      actions: ["capability.search"]
      resources:
        - namespace: engineering
          repository: api
```

The two grants above are deliberately provider-neutral after authentication. The authorization package does not know whether `Skillet.Read` came from `scp` or `Skillet.Read.All` came from `roles`; it only evaluates normalized permissions, actions, and authoritative resource scope.

For broader access, add explicit grants for the required actions and namespace/repository boundaries rather than treating request filters as authority.

## Claim mapping policy

| Entra access-token claim | Skillet meaning | Policy |
| --- | --- | --- |
| `sub` | `Identity.Subject` | Required by the generic JWT validator. Do not use display names, UPNs, or email addresses as stable authorization identity. |
| `tid` | `Identity.OrganizationID` | Configure `organization_claim: tid`. For this single-tenant profile, `organization.id`, resource organisation IDs, and the trusted `tid` value must match exactly. |
| `scp` | delegated permissions and compatibility scopes | Configure `scope_claim: scp`. Only explicitly granted values should appear in authorization grants. |
| `roles` | workload/application permissions | Configure `role_claim: roles`. Only explicitly granted app-role values should appear in authorization grants. |
| `groups` | optional configured attribute | Not part of the recommended committed profile; see group overage below. |

There is no tenant lookup in authorization code. Issuer validation authenticates the configured tenant boundary, `tid` is normalized as organisation identity, and the existing exact organisation check prevents a trusted identity from crossing into another organisation's resources.

If a deployment needs an organisation identifier that is not the Entra tenant ID, that requires an explicit, separately designed trusted mapping boundary. Do not add ad-hoc Microsoft-specific tenant translation inside domain authorization.

## Delegated user tokens

A delegated access token should contain the configured issuer/audience, a stable subject, the tenant ID, and one or more delegated scopes, for example:

```json
{
  "iss": "https://login.microsoftonline.com/<tenant-id>/v2.0",
  "aud": "api://<skillet-api-application-id>",
  "sub": "<user-subject>",
  "tid": "<tenant-id>",
  "scp": "Skillet.Read"
}
```

After signature, issuer, audience, lifetime, and claim-shape validation, `Skillet.Read` is present in the normalized permission set. Claims policy can then authorize only the configured Skillet actions and namespace/repository resources.

## Workload/service-principal tokens

Client-credential/application tokens use app roles rather than delegated scopes:

```json
{
  "iss": "https://login.microsoftonline.com/<tenant-id>/v2.0",
  "aud": "api://<skillet-api-application-id>",
  "sub": "<service-principal-subject>",
  "tid": "<tenant-id>",
  "roles": ["Skillet.Read.All"]
}
```

`roles` is normalized into the same `Identity.Permissions` set used for delegated scopes. Authorization therefore does not need a service-principal-specific policy implementation.

## Deterministic deny behaviour

The profile fails closed at the existing boundaries:

| Case | Boundary | Result |
| --- | --- | --- |
| token issuer differs from the configured tenant issuer | generic JWT/OIDC authentication | deny |
| token audience differs from the configured API audience | generic JWT/OIDC authentication | deny |
| token signature does not verify against discovered JWKS | generic JWT/OIDC authentication | deny |
| normalized `tid` differs from the target resource organisation | generic authorization policy | deny with organisation mismatch |
| delegated `scp` or workload `roles` value has no matching grant | claims authorization policy | deny as not entitled |
| namespace is outside the matching grant | claims authorization policy | deny as not entitled |
| repository is outside the matching grant | claims authorization policy | deny as not entitled |

Repository/namespace request filters never create authority. They may narrow a request, but every disclosed resource is still checked against trusted identity entitlements and authoritative resource scope.

## Group overage and Microsoft Graph

Entra can emit group IDs in a `groups` claim, but JWT group membership is subject to an overage limit. When the limit is exceeded, Entra can omit the full `groups` list and emit distributed/overage claims that direct an application to retrieve membership from Microsoft Graph. See Microsoft's claims documentation: <https://learn.microsoft.com/en-us/entra/identity-platform/access-token-claims-reference#groups-overage-claim>.

Skillet deliberately does **not** follow that pointer or call Microsoft Graph in this profile. The reasons are architectural and operational:

- app scopes and app roles are sufficient for the committed delegated/workload authorization profile;
- resolving group overage would introduce a provider-specific network dependency, credential lifecycle, caching, failure semantics, and directory-consistency concerns into the runtime authorization path;
- an omitted configured `groups` attribute grants nothing, so a group-dependent authorization rule fails closed rather than silently broadening access.

If a future requirement genuinely needs directory-backed group resolution, treat it as a separate identity-enrichment capability with its own threat model, availability contract, cache policy, tests, and evidence. Do not hide Microsoft Graph access inside the generic OIDC validator or domain policy.

## Local proof

`internal/auth/entra_compatibility_e2e_test.go` models the Entra claim shapes without live Entra or external network access. An in-memory HTTP transport serves OIDC discovery and JWKS responses, while locally generated RSA keys sign the tokens.

The proof covers both delegated and workload identities through:

```text
Entra-shaped JWT
  -> generic OIDC discovery/JWKS validator
  -> normalized auth.Identity
  -> generic authorization.ClaimsPolicy
  -> organisation / namespace / repository allow or deny
```

Run the focused proof with:

```bash
go test ./internal/auth -run Entra
```

The repository acceptance gate must also remain green:

```bash
go run ./cmd/skillet-verify
```

No Microsoft Graph package, Entra SDK, or provider-specific core authorization dependency is required by this deployment profile.
