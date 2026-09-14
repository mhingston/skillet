# ADR-015: M3 human UI, collaboration, composition, and distribution boundaries

## Status

Accepted for the vNext M3 architecture.

This ADR defines the product, security, and source-of-truth boundaries for roadmap issue #67 and its M3 child issues. It is a documentation contract only and does not change runtime behaviour, public APIs, storage, ranking, governance, authorization, or materialisation semantics by itself.

ADR-013 remains authoritative for vNext domain/module ownership and ADR-014 remains authoritative for authentication and authorization. This ADR adds a human-facing adapter and bounded collaborative/operational concepts without replacing either contract.

## Context

M1 established separate capability, knowledge, governance, evidence, and materialisation concerns inside one Go modular monolith. M2 established a provider-neutral authentication boundary and a transport-neutral `AuthorizationPolicy` that must authorize existing product operations using trusted `auth.Identity` and authoritative resource scope.

M3 introduces a human-facing browser surface and considers collaboration, deterministic dependency/composition metadata, distribution adapters, and improvement proposals. These features create several risks if their boundaries are not frozen first:

- a browser UI could become a second application with duplicated authorization, ranking, governance, or materialisation rules;
- browser state could become an accidental new source of truth for capability or knowledge content;
- comments, watches, or usefulness signals could silently influence semantic relevance;
- dependency/composition support could drift into LLM-invented workflow execution;
- distribution could become a second publishing authority rather than an export of immutable approved revisions;
- improvement tooling could bypass review by rewriting or activating canonical source;
- frontend convenience could weaken M2 authorization or introduce avoidable browser security exposure.

M3 therefore needs an explicit adapter/state ownership contract before runtime implementation begins.

## Decision

### M3 extends the existing modular monolith; it does not create a second application

The production system remains one Go modular monolith as defined by ADR-013.

The human web UI is an **adapter over existing application/domain services**. It may translate browser requests into existing use cases and render their results, but it does not own or duplicate:

- authentication trust decisions;
- `AuthorizationPolicy` decisions;
- capability or knowledge ranking;
- governance visibility/lifecycle rules;
- immutable revision identity;
- package/materialisation rules;
- evidence derivation rules;
- dependency resolution semantics;
- distribution approval rules.

Any product rule needed by both MCP/HTTP and the browser belongs in the owning application/domain boundary and is called from both adapters. A browser-only copy of a domain rule is a bug even when it produces the same result today.

The target dependency direction is:

```text
browser request
      |
      v
human web adapter (handlers + templ views)
      |
      +--> existing authentication boundary -> trusted auth.Identity
      |
      +--> existing AuthorizationPolicy
      |
      +--> application/domain use cases
                  |
                  +--> capability / knowledge / governance / evidence
                  +--> materialisation / immutable revision contracts
                  +--> deterministic composition service when introduced
                  +--> distribution application ports when introduced
```

The UI adapter may own presentation-specific view models and browser security/session mechanics. It must not become an alternate domain model.

### Frontend stack is intentionally small and server-driven

The M3 browser surface uses:

- Go `templ` for HTML rendering;
- standard Go HTTP handlers/middleware;
- pinned and vendored htmx 2.x served from embedded application assets;
- plain CSS and CSS custom properties;
- embedded static assets;
- minimal handwritten JavaScript only when a browser capability cannot be expressed cleanly with HTML, CSS, or htmx progressive enhancement.

Production does **not** require Node, npm, a Next.js runtime, a SPA framework, hydration, or a client-side application state store.

Build/release of the Go binary must remain sufficient to ship the browser UI. Vendored frontend assets are version-pinned and included in the repository/binary so a production page does not depend on a public CDN.

### Progressive enhancement is a product constraint

The server owns navigation and application state. Browser URLs identify useful resources/views, and ordinary navigation or form submission must remain understandable without a client-side application state model.

htmx may improve partial navigation, inline updates, and interaction latency, but every htmx interaction maps to a normal server-side use case with deterministic authorization and validation.

Where practical, an equivalent full-page request/response path must exist. A feature may require JavaScript only when the browser capability itself requires it; JavaScript must not become the sole holder of authorization, governance, ranking, composition, or workflow state.

Back/forward navigation, reloads, and bookmarked detail/search URLs must not depend on hidden SPA state for correctness.

### Commonplace is interaction prior art, not a product model

Commonplace may inform interaction patterns such as:

- a compact application shell;
- sidebar navigation;
- prominent search;
- breadcrumbs;
- metadata summaries;
- dense list/detail views;
- readable relationship/context panels.

Skillet is **not** becoming a wiki editor. Git repositories and supported OKF inputs remain authoritative for canonical capability/knowledge source. The UI may expose source provenance and links/handoffs to the owning workflow, but it must not imply that browser edits supersede Git/OKF authority.

### Browser content is untrusted data

Capability metadata, knowledge snippets, tool metadata, discussion text, proposal descriptions, repository names, namespaces, user display values, and other content rendered into HTML are untrusted data regardless of whether they originated from Git, OKF, an identity token, or Skillet-owned storage.

The browser adapter therefore follows these rules:

1. `templ`/Go HTML escaping is the default rendering path.
2. Domain strings are rendered as text, not as trusted HTML.
3. Raw HTML supplied by skills, knowledge documents, MCP tool metadata, discussion content, or proposal content is never injected into templates.
4. If a later issue renders Markdown, raw embedded HTML is disabled or removed and output is passed through one centrally configured sanitizer before it reaches a trusted-HTML boundary.
5. URLs derived from untrusted content are parsed and validated for allowed schemes before being rendered as links.
6. No UI feature may require `unsafe-eval`; avoid `unsafe-inline` by serving scripts/styles as embedded static assets or using a narrowly documented nonce/hash mechanism if a future browser requirement proves unavoidable.

Escaping/sanitization is a presentation security concern. It does not mutate the authoritative stored/source value merely to make it displayable.

## Existing M2 identity and authorization remain authoritative

### One authorization contract for MCP, HTTP, and browser actions

ADR-014's trusted identity and `AuthorizationPolicy` remain authoritative for all browser routes/actions.

The browser adapter must:

1. authenticate through the existing configured authentication boundary;
2. receive/use the normalized trusted `auth.Identity` rather than inspecting provider-specific claims in handlers/templates;
3. map the requested operation to the same typed authorization action/resource semantics used by the application boundary;
4. invoke `AuthorizationPolicy` before sensitive disclosure or mutation;
5. call the owning use case only after authorization succeeds.

Templates must not decide whether an action is authorized by examining roles, claims, groups, namespaces, or repository names directly. Hiding a button is presentation only and never substitutes for server-side authorization.

A browser route is not implicitly trusted because it is interactive, same-origin, or reached after login.

### Session and cookie expectations

The browser adapter may maintain the minimum session state required to bridge browser navigation to the existing authenticated identity boundary. Any implementation must preserve the following contract:

- browser authentication/session state is adapter-owned security state, not domain authority;
- cookies carrying session identifiers or security-sensitive state are `HttpOnly` and `Secure` in production;
- `SameSite=Lax` is the default unless a specific authenticated flow requires a different setting and documents the threat-model trade-off;
- cookie scope is restricted to the narrowest useful host/path and must not be available to unrelated applications by default;
- session identifiers have sufficient entropy, are not accepted from URL query parameters, and are rotated when an authentication transition would otherwise permit fixation;
- logout invalidates server-side session state or otherwise makes the session unusable according to the chosen bounded session design;
- raw bearer tokens, provider refresh tokens, or arbitrary verified-claim payloads are not exposed to browser JavaScript or persisted in local/session storage;
- development-only relaxation for localhost must be explicit and must not silently become the production default.

This ADR does not require a new persistent user directory. Session state maps an already-authenticated browser interaction to a trusted identity; it does not create identity authority.

### CSRF

Every state-changing browser action uses a CSRF defense appropriate to the chosen server-side session mechanism.

Requirements:

- `GET`, `HEAD`, and other safe navigation endpoints do not mutate authoritative/collaborative state;
- unsafe methods/forms require an unguessable session-bound CSRF token (for example synchronizer-token or equivalently strong server-validated design);
- htmx requests carry the same protection rather than receiving a CSRF exemption;
- same-site cookies and `Origin`/`Referer` validation may provide defense in depth but do not replace the explicit token for cookie-authenticated state-changing requests;
- failed CSRF validation fails closed before the owning use case is invoked.

### Clickjacking and framing

The browser adapter is not intended to be embedded by arbitrary origins.

Responses set a restrictive framing policy, preferably CSP `frame-ancestors 'none'`, with `X-Frame-Options: DENY` retained where useful for legacy defense in depth. A future embeddable integration requires a separate decision and explicit allowlist.

### CSP and static assets

The baseline Content Security Policy is self-origin only and should be implementable without exceptions for the selected stack. The intended baseline is equivalent to:

```text
default-src 'self';
base-uri 'self';
frame-ancestors 'none';
form-action 'self';
object-src 'none';
script-src 'self';
style-src 'self';
img-src 'self' data:;
connect-src 'self';
```

Exact directives may be tightened or minimally extended by an implementation issue when required by a documented feature. htmx and application JavaScript/CSS are served from pinned embedded assets under the same origin. Production must not rely on third-party CDN script execution.

Static assets use content types that match their actual type and should be served with `X-Content-Type-Options: nosniff`. Cache policy may be long-lived for content-addressed/versioned assets but must not cause authenticated HTML or sensitive data to be cached as public immutable content.

### Redirects

Redirect targets influenced by request input are validated before use.

- Prefer internal relative paths for post-login/post-action redirects.
- Absolute redirects, if ever required, use an explicit configured allowlist of origin(s).
- Scheme-relative URLs, backslash ambiguities, encoded-host tricks, and arbitrary `return_to`/`next` values must not create open redirects.
- Authentication callback handling continues to follow the existing authentication boundary; the UI adapter does not invent provider-specific redirect validation.

### Additional browser response expectations

The browser adapter should set a conservative referrer policy and avoid leaking sensitive route/query information cross-origin. Sensitive authenticated pages should not be framed or MIME-sniffed and should receive cache controls appropriate to their content.

These controls form part of the browser adapter's threat model and should be covered by deterministic handler/security tests when M3 runtime implementation begins.

## Source-of-truth ownership

M3 introduces new presentation and collaboration concepts, but it does not move canonical capability or knowledge ownership into Skillet.

| State / concept | Authoritative owner | Skillet may persist/derive | Must not do |
| --- | --- | --- | --- |
| Capability definition and source-owned metadata | Canonical source repository / supported ingestion input | Indexed projection, provenance, immutable revision/package identity, source link | Treat browser edits/comments as canonical definition |
| Knowledge document content and source metadata | Git/OKF source accepted by the knowledge ingestion boundary | Indexed chunks/provenance/freshness/explicit links | Turn UI discussion into canonical knowledge automatically |
| Governance visibility/lifecycle/ownership policy | Existing Skillet governance state/rules defined by M1/M2 | Existing authoritative governance records/audit | Let UI metadata or popularity bypass governance transitions |
| Identity and authorization | Existing authentication boundary + `AuthorizationPolicy` | Browser session/security bridge to trusted identity | Build a new user/group identity authority in M3 |
| Dependency/composition declarations | Source-owned capability metadata/manifest for the relevant immutable revision | Parsed/validated deterministic composition projection and resolved plan/lock data | Invent undeclared dependencies at runtime or via LLM |
| Discussion threads/comments | Skillet-owned collaborative state linked to stable resource/revision identity | Text, author/identity reference, timestamps, status as required | Rewrite source, governance, or ranking as a side effect |
| Watch/subscription state | Skillet-owned per-identity operational state | Resource watch preferences and delivery cursor/state if later introduced | Change relevance, approval, or lifecycle |
| Usefulness signals | Skillet-owned evidence/collaboration state tied to immutable provenance where applicable | Explicit user signal and bounded aggregates | Affect semantic ranking by default or silently become approval |
| Improvement proposal | Skillet-owned reviewable proposal/evidence state plus generated handoff/patch artifact | Proposal status, evidence links, deterministic patch/handoff artifact, external review reference | Directly rewrite/activate canonical source or silently apply a fix |
| Distribution configuration | Deployment/operator configuration plus adapter-specific non-secret settings | Adapter config and bounded delivery policy | Become content/source authority |
| Distribution record | Skillet-owned operational/audit record referencing an immutable approved revision | Destination, immutable revision/package identity, outcome, timestamps | Create a new mutable semantic version of the exported content |
| Browser session / CSRF state | Human web adapter security state | Minimal session and anti-forgery data | Become identity, governance, or domain authority |
| UI preferences | Optional Skillet-owned presentation state | Non-security preferences such as density/theme if introduced | Affect authorization or semantic ranking |

When source-owned metadata and Skillet-owned collaborative state disagree, the owning source/domain contract wins. Collaboration may surface the disagreement or propose a reviewable change; it may not silently reconcile by mutating canonical source.

## Collaboration boundary

Collaboration exists to help humans understand and improve resources without redefining their semantics.

Discussion, watch state, and usefulness signals are separate from semantic retrieval. By default they do **not**:

- enter embedding/routing text;
- modify lexical/vector scores;
- alter reranker inputs;
- boost popularity;
- change governance approval/lifecycle;
- replace source metadata;
- select a newer revision during materialisation.

If future evidence demonstrates that a collaborative signal should affect ranking or governance, that requires a separate explicit issue/decision, protected evaluation fixtures, and a migration plan. It must not arrive accidentally as part of UI implementation.

Collaborative records reference stable resource identity and, where the statement concerns concrete content/behaviour, the immutable revision/package provenance needed to avoid ambiguity after source evolution.

## Deterministic dependency and composition boundary

M3 may add declarative capability dependencies/composition, but composition means **deterministically resolving declared relationships**, not generating a workflow.

### Source-owned declarations

Dependencies/composition declarations belong to source-owned capability metadata for an immutable revision. The exact schema is owned by its implementation issue, but it must be explicit and machine-validated.

A declaration may identify required/optional compatible capabilities or other bounded composition metadata. It must not contain an instruction to let an LLM search for whatever else seems useful at runtime.

### Resolution properties

Given the same:

- root immutable revision(s);
- source-owned declarations;
- catalogue/governance state relevant to eligibility;
- authorization context where disclosure/acquisition is requested;
- resolver version/configuration;

resolution must be deterministic.

The resolver must have explicit rules for at least:

- stable identity and revision selection/pinning;
- missing dependencies;
- ambiguous matches;
- cycles;
- incompatible constraints;
- authorization/governance denial;
- deterministic ordering/tie-breaking;
- maximum depth/size bounds needed to avoid unbounded expansion.

Failures are explicit and fail closed; the resolver does not ask an LLM to repair or invent a dependency graph.

### Composition produces data, not execution

Composition may produce a reviewable/resolvable plan, dependency graph, lock-like structure, or materialisation set. It may prepare/acquire immutable approved packages through the existing materialisation boundary.

It does not:

- invoke tools;
- execute skill scripts;
- orchestrate steps/workflows;
- hold downstream credentials;
- decide at runtime which undeclared capability an LLM should add;
- bypass the consuming harness's responsibility for actual execution.

This preserves ADR-013's invariant that materialisation is not execution.

## Distribution boundary

Distribution adapters are **export adapters over approved immutable revisions**.

They may package/copy/publish a revision to a supported destination using the immutable identity already established by capability/materialisation/governance boundaries. Before export, the application layer must establish that the requested revision is eligible for distribution according to existing governance/authorization rules.

A distribution adapter:

- receives an explicitly selected immutable revision/package and bounded destination configuration;
- verifies/uses the existing immutable digest/provenance contract rather than rebuilding semantic identity;
- exports bytes/metadata in a deterministic destination-specific form where possible;
- records a bounded operational/audit result;
- may retry according to explicit idempotency semantics.

A distribution adapter does **not**:

- become canonical source authority;
- accept arbitrary browser-edited content as a new revision;
- silently select `latest` when an immutable revision was requested;
- mutate the source repository;
- alter governance approval/lifecycle;
- weaken lockfile or historical restoration semantics;
- execute the distributed capability.

Destination-generated IDs/URLs are delivery metadata only. They do not replace Skillet's canonical immutable revision/package identity.

If a destination supports mutable aliases such as `latest`, the adapter may update such an alias only as an explicit delivery action while retaining the exact exported immutable revision in Skillet's distribution record. Reproducibility always refers to the immutable revision, never the alias.

## Improvement proposal boundary

Improvement proposals extend ADR-013's evidence boundary: observed evidence may lead to a **reviewable proposed change**, but never autonomous source mutation.

An improvement proposal may contain:

- the stable capability/knowledge identity;
- immutable revision/provenance being discussed;
- linked evidence/discussion;
- a human/agent-authored rationale;
- a deterministic or generated patch/handoff artifact;
- review/status metadata;
- an external source-control issue/PR reference if created through an explicitly authorized workflow.

A proposal may help produce a patch or handoff for the canonical source workflow. Applying, merging, publishing, approving, or activating that change remains a separate explicit action in the source/governance system.

The proposal path must not directly:

- rewrite canonical Git/OKF source;
- change an active immutable revision in place;
- mark a proposal as approved merely because it was generated;
- activate a new revision without ordinary ingestion/governance checks;
- turn model output into trusted HTML or executable workflow state.

Automatic issue/PR creation is not implied by this ADR. If introduced later, it is an outbound integration action with explicit authorization/idempotency/audit requirements and does not make Skillet the source authority.

## Browser threat model

M3 browser implementation must explicitly defend the following classes of failure.

### Cross-organisation or cross-scope disclosure

A human UI can make enumeration easier than an API, so route parameters, search filters, breadcrumbs, autocomplete, counts, links, and error messages must all respect the same M2 authorization boundary. A caller must not infer unauthorized resource existence from UI-only metadata.

### Authorization bypass through alternate routes

Full-page, htmx partial, form, JSON/download, and direct deep-link routes must converge on the same authorization/use-case boundary. A hidden navigation element or inaccessible normal route must not leave an unguarded partial/download endpoint.

### Stored/reflected XSS

All rendered source/collaboration content is untrusted. Auto-escaping, no raw metadata HTML, sanitizer boundaries for any future rich text, self-hosted scripts, and restrictive CSP are required.

### CSRF

Cookie-authenticated mutations require explicit anti-forgery validation before domain/application mutation.

### Clickjacking

Authenticated or state-changing views are not frameable by arbitrary origins.

### Session fixation/theft

Session identifiers are high-entropy, scoped, secured by cookie attributes, rotated at authentication transitions as required, never accepted through URLs, and do not expose provider credentials to JavaScript.

### Open redirect / auth-flow confusion

User-controlled navigation targets cannot redirect to arbitrary origins or alter the trusted authentication callback contract.

### UI/domain rule drift

Presentation code can accidentally duplicate policy and diverge over time. The architectural defense is dependency direction: handlers call authoritative use cases/policy; templates receive already-evaluated view models and do not implement domain decisions.

### Collaborative-state poisoning

Comments, watches, usefulness signals, and proposals are untrusted collaborative data and do not enter semantic ranking, governance, or source automatically.

### Composition amplification

A malicious/incorrect dependency graph could create unbounded expansion or unexpected acquisition. Resolution is deterministic, bounded, cycle-aware, and constrained by authorization/governance/materialisation checks.

### Distribution confusion

Exports are bound to exact approved immutable revisions. Destination aliases or mutable destination state cannot be treated as proof of what revision was exported.

## Human UI/application boundary

A browser handler should be thin and follow this sequence:

1. parse and size-bound route/query/form input;
2. establish trusted authenticated identity through the existing authentication/session boundary;
3. invoke the existing `AuthorizationPolicy` through the owning application use case;
4. call the domain/application service;
5. map the result into a presentation-only view model;
6. render escaped HTML or a bounded redirect/response.

Mutation handlers additionally validate CSRF before invoking the mutating use case.

The browser package may contain:

- route registration;
- session/CSRF/browser-security middleware;
- request/form parsing and presentation validation;
- `templ` components/pages;
- presentation-only view models;
- embedded frontend assets;
- mapping of application errors to safe HTTP/browser responses.

It must not contain competing implementations of:

- search/ranking;
- visibility/governance eligibility;
- authorization rules;
- dependency resolution;
- package selection/digest verification;
- evidence candidate derivation;
- distribution approval semantics.

## Deliberately out of scope

The following remain explicitly outside M3 unless a separate evidence-backed issue/ADR changes the boundary:

- runtime MCP/tool execution or a dynamic execution broker;
- custody/delegation of downstream execution credentials;
- arbitrary workflow orchestration;
- LLM-invented runtime capability composition/dependency graphs;
- public social marketplace semantics, public profiles, follower/reputation systems, or popularity-driven discovery;
- ratings/social signals affecting semantic ranking by default;
- a built-in identity provider, persistent user/group directory, SCIM authority, or replacement for external OIDC providers;
- browser editing becoming canonical capability/knowledge authoring;
- automatic activation or direct rewriting of canonical source from an improvement proposal;
- a Node/Next production runtime or SPA state layer;
- distribution destinations becoming source authority.

## Verification obligations for later M3 implementation

This ADR itself is documentation-only. Runtime M3 slices must preserve the existing authoritative verification gate and add proof specific to their contracts.

Expected deterministic coverage includes, as features are implemented:

- browser routes call the existing authorization boundary and cannot disclose cross-organisation/cross-scope resources;
- full-page and htmx paths produce equivalent authorization/domain outcomes;
- rendered source/collaboration metadata is escaped and raw HTML cannot execute;
- state-changing cookie-authenticated requests fail without valid CSRF protection;
- baseline CSP/frame/content-type/cache/security headers are present where applicable;
- redirects reject unsafe external targets;
- progressive enhancement preserves functional full-page navigation/forms for core flows;
- collaborative signals do not change protected semantic ranking fixtures;
- deterministic composition produces stable output for stable inputs, rejects cycles/ambiguity according to explicit rules, and never executes capabilities;
- distribution binds records/exports to exact approved immutable revisions;
- improvement proposals cannot mutate/activate canonical source as a side effect.

The existing repository gate remains:

```sh
go run ./cmd/skillet-verify
```

No new architecture-lint mechanism is required merely to enforce this ADR.

## Consequences

Positive consequences:

- humans gain a first-class surface without creating a second implementation of product/security policy;
- the UI ships with the Go product and preserves local/self-contained deployment;
- M2 authorization remains the single policy authority across agent and human transports;
- collaboration can evolve without silently changing retrieval relevance or source truth;
- composition can improve reuse while retaining deterministic/reproducible acquisition semantics;
- distribution can target external destinations without weakening immutable provenance;
- improvement tooling can accelerate source changes while preserving review and source ownership;
- browser security expectations are explicit before routes/state are added.

Trade-offs:

- server-driven progressive enhancement requires deliberate URL/form/view-model design instead of relying on a client state store;
- authorization must be threaded through each reachable browser action rather than assumed from navigation visibility;
- collaborative state needs explicit stable/revision references and cannot cheaply reuse ranking metadata;
- deterministic composition is less flexible than ad-hoc LLM orchestration but is reproducible and testable;
- distribution aliases/convenience features must retain immutable revision bookkeeping;
- proposals require an external review/apply step before canonical source changes.

## Explicit M3 invariants

1. The human web UI is an adapter, not a new domain/application authority.
2. Existing authentication and `AuthorizationPolicy` remain authoritative for every browser action.
3. Browser templates/handlers do not duplicate ranking, governance, authorization, materialisation, composition, or distribution policy.
4. Production UI uses Go `templ`, standard HTTP, pinned/vendored htmx 2.x, plain CSS, embedded assets, and minimal JavaScript; no Node/Next runtime or SPA state model is required.
5. Progressive enhancement does not depend on client-side application state for correctness.
6. Rendered product/source/collaboration data is untrusted and escaped/sanitized through a single explicit presentation boundary; raw metadata HTML is never trusted.
7. Browser session/CSRF state is adapter security state and never becomes identity/domain authority.
8. Git/OKF remain canonical source for capability/knowledge content; Skillet-owned collaboration cannot silently override them.
9. Discussion/watch/usefulness state does not affect semantic ranking or governance by default.
10. Dependency/composition declarations are source-owned, declarative, bounded, and deterministically resolved; composition produces data/materialisation plans, not execution.
11. Distribution exports exact approved immutable revisions and never becomes source authority.
12. Improvement proposals can produce reviewable patches/handoffs but cannot directly rewrite or activate canonical source.
13. Runtime tool execution, credential custody, workflow orchestration, public social marketplace semantics, and built-in identity remain out of scope.
14. ADR-013 and ADR-014 remain unchanged; any future conflict requires an explicit reviewed architecture decision rather than an implicit UI exception.
