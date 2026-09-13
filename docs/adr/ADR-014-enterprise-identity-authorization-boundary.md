# ADR-014: enterprise identity and authorization boundary

## Status

Accepted for the vNext M2 architecture.

This ADR defines the security and module boundary for roadmap issue #51 and its child issues. It does not change runtime behaviour by itself.

## Context

M1 already authenticates static credentials or OIDC/JWT access tokens and produces a trusted `auth.Identity` containing a subject, organisation, normalized delegated scopes, and a copy of verified claims. M1 also has first-class organisation / namespace / repository scope metadata for capabilities and knowledge, plus durable local audit events.

M2 must make those existing boundaries usable in enterprise deployments without turning Skillet into an identity platform or coupling it to Microsoft Entra, Microsoft Graph, SCIM, a specific SIEM, or a generic workflow/RBAC engine.

The central security problem is therefore not provider-specific authentication. It is deciding whether one already-authenticated human or workload identity may perform one existing Skillet action against one scoped resource.

## Decision

### Authentication remains the trust boundary

The existing authentication packages remain responsible for validating credentials, token signatures, issuer, audience, time claims and configured trusted claims. Provider discovery/JWKS resolution remains outside domain/application policy.

After successful authentication, downstream code may trust only the normalized `auth.Identity` produced by that boundary. Raw request headers, unsigned request fields and unverified token data are not authorization inputs.

M2 does not add an Entra-specific authentication stack. Entra, Okta, Auth0 and other compliant providers are deployment profiles over the generic OIDC/JWT boundary.

### Authorization is a separate transport-neutral policy

M2 introduces a small independently testable authorization policy boundary. Conceptually it evaluates:

- trusted identity;
- typed action;
- minimum scoped resource metadata;
- an allow/deny decision with a bounded reason suitable for diagnostics/audit.

The concrete Go API is owned by the implementation slice and must be derived from existing call sites rather than this ADR's wording.

The policy must not depend on HTTP, MCP, JWT libraries, provider SDKs, semantic indexes, or storage implementation details. HTTP/MCP adapters authenticate a request, retain the trusted identity in context, construct the relevant action/resource, invoke the policy, and then call the owning domain service only when allowed.

### Resource scope is explicit

Authorization resources carry only the information needed to make the decision:

1. organisation;
2. optional namespace;
3. optional repository;
4. optional stable capability / knowledge / evidence / governance identity where the operation needs it.

Missing or forged request scope can never broaden access. A caller may narrow a request to a subset it already possesses, but a repository or namespace supplied by the caller is not itself proof of entitlement.

### Actions correspond to existing product operations

M2 authorization covers existing Skillet operations only. Initial actions are expected to include capability search/describe/materialize, knowledge search/read, evidence report/review and governance read where those operations already exist.

M2 does not introduce generic object CRUD, workflow execution, dynamic tool brokering, credential delegation or a general policy language.

### Identity normalization is provider-neutral

Delegated users and workload identities converge on one normalized identity model before policy evaluation.

Configured verified claims may contribute normalized permissions/attributes such as:

- delegated OAuth/OIDC scopes (`scope`, `scp`, or configured equivalent);
- application/workload roles (`roles` or configured equivalent);
- optional group/entitlement values only when explicitly configured.

Application/domain code must not check Entra claim names directly. Missing optional claims grant nothing. A malformed configured claim fails closed instead of being ignored in a way that broadens access.

Provider-specific group-overage or directory lookup mechanisms are not silently introduced. If a deployment cannot express required authorization in bounded trusted token claims, that becomes a separate evidence-backed integration decision.

### Compatibility policy is the default

Existing development/static/OIDC deployments remain simple. Enterprise claims authorization is opt-in.

The built-in/default policy preserves the current organisation isolation and documented scope behaviour. Enabling enterprise authorization must not be required merely to run Skillet locally or in a small trusted team.

### Authorization and retrieval ranking remain separate

ADR-013 remains authoritative: visibility is an authorization/catalogue constraint, not semantic relevance.

Authorization metadata, roles, groups, tenant identifiers, namespace permissions and repository entitlements must not be embedded, appended to retrieval queries, or used as semantic reranking text merely to enforce access.

The allowed-set computation and semantic ranking are separate concerns. Filtering may reduce the eligible result set, but among resources that are already authorized the protected semantic ranking contract must not change because of enterprise authorization metadata.

### Materialisation is still not execution

ADR-001, ADR-003 and ADR-013 continue to apply. Authorization may deny discovery, selection, materialisation preparation or package acquisition for a caller that is not entitled to the resource. It does not weaken immutable revision/package identity or historical lock reproducibility.

A lock, candidate token, package digest, repository identifier or signed package URL is not itself an authorization entitlement. The request must still pass the applicable authenticated policy boundary.

### Audit export is secondary to authoritative state

Existing local audit/event state remains authoritative where Skillet already records it. M2 may add an optional bounded audit sink/export interface for forwarding normalized events to existing observability/SIEM infrastructure.

Export is best-effort/operational unless a future event explicitly documents durable external delivery as part of its transaction contract. Exporter failure must not roll back, mutate or corrupt catalogue, governance, evidence, materialisation/package or other authoritative business state.

Exported events must be bounded and must not include bearer tokens or arbitrary verified claim payloads by default.

## Threat model and required controls

### Cross-organisation / cross-tenant leakage

A valid identity from organisation A must never gain access to organisation B because it supplies B's namespace, repository, resource ID or request filter. Organisation isolation is checked independently of semantic retrieval.

Deterministic M2 fixtures must demonstrate zero cross-organisation leakage.

### Forged namespace/repository context

Namespace/repository values supplied in an MCP/HTTP request are selectors, not authority. Policy uses trusted identity entitlements plus authoritative resource provenance/scope. Forged or unknown selectors cannot broaden visibility.

### Confused deputy

Skillet must not use its own access to repositories, packages, knowledge or audit state to perform an operation for a caller that the caller is not authorized to request. Authorization is evaluated at the application boundary before disclosure or materialisation and again where an independently reachable path could otherwise bypass it.

### Malformed or over-broad claims

Configured permission/role/group claims are strictly normalized. Wrong types, malformed values, missing mappings and unknown roles do not become wildcard access. Unmapped claims grant nothing.

### Provider claim overage / external directory dependency

Claims that indicate omitted/over-limit groups do not trigger implicit Microsoft Graph or another directory lookup. Such a token cannot acquire permissions not present in the normalized trusted model. Any future directory enrichment must be a separately reviewed optional adapter with explicit failure semantics.

### Materialisation/package bypass

Stable IDs, lock entries, candidate tokens, signed URLs and immutable digests do not substitute for authorization. Existing cryptographic/reproducibility checks remain necessary but are not sufficient to establish caller entitlement.

### Audit exporter failure coupling

An unavailable or failing exporter cannot change the result of an otherwise valid catalogue/evidence/governance write unless a future contract explicitly opts into durable external delivery. Export degradation is observable through deterministic logging/metrics/tests.

## Module/dependency direction

M2 extends ADR-013's modular-monolith direction:

```text
OIDC/static authentication
        |
        v
 trusted auth.Identity
        |
        v
 internal/authorization policy
        |
        +---- capability / materialisation
        +---- knowledge
        +---- evidence
        +---- governance

local authoritative audit state
        |
        +---- optional audit sink/export adapter
```

The authorization package may depend on small shared identity/scope value types. It must not depend on transport, provider SDKs, semantic retrieval implementations or concrete SIEM clients.

Domain services and edge adapters may depend on the authorization contract as required by their call paths. Retrieval mechanics remain unaware of identity/role/entitlement data.

## Deliberately deferred

The following remain outside committed M2 scope unless a separate evidence-backed issue changes the decision:

- SCIM provisioning/reconciliation;
- persistent local user/group directory semantics;
- Microsoft Graph runtime dependency;
- provider-specific SIEM integrations beyond the generic sink contract;
- web admin / marketplace UI;
- per-user install credentials unrelated to authorization;
- dynamic MCP execution or delegated downstream credentials;
- distributed storage/search introduced only for enterprise branding rather than scale evidence.

## Verification obligations

Every runtime M2 slice must preserve the M1 release gate and add deterministic proof for its own contract. The integrated M2 gate must cover at least:

- compatibility behaviour for development/static auth modes;
- delegated user and workload identities through real local JWT/OIDC fixtures;
- allow/deny matrices across organisation / namespace / repository boundaries;
- zero deterministic cross-organisation leakage;
- fail-closed missing/malformed/untrusted claims;
- authorization metadata having no protected semantic-ranking effect;
- materialisation/read denial cases;
- audit exporter degradation without authoritative-state corruption;
- machine-readable CI evidence.

"Provably correct" continues to mean that these observable isolation and authorization contracts are executable and regression-gated, not that the system is formally verified.

## Consequences

Positive consequences:

- enterprise deployment reuses the existing OIDC trust boundary;
- provider-specific behaviour stays primarily in configuration/claim mapping;
- local deployments retain a low-complexity default;
- policy is testable without standing up MCP/HTTP or a live identity provider;
- authorization cannot silently become a relevance/ranking feature;
- audit integration remains replaceable and operationally isolated.

Trade-offs:

- authorization must be threaded through several existing call paths rather than hidden in one HTTP middleware;
- resource provenance/scope must be available before sensitive disclosure/materialisation decisions;
- claim normalization must be strict, which can reject misconfigured provider tokens rather than attempting permissive interpretation;
- deployments that require directory enrichment beyond token claims need a later explicit integration decision rather than receiving an implicit Graph/SCIM dependency.
