package httpserver

const operatorTemplateSource = `{{define "operator-quarantine-card"}}
<article class="result-card">
  <div class="card-heading"><div><span class="badge state-yanked">quarantined</span></div></div>
  <h3>{{if .Name}}{{.Name}}{{else}}{{.Path}}{{end}}</h3>
  <dl class="compact-meta">
    <div><dt>Skill</dt><dd><code>{{.SkillID}}</code></dd></div>
    <div><dt>Revision</dt><dd><code>{{short .RevisionID}}</code></dd></div>
    <div><dt>Repository</dt><dd>{{.RepositoryID}}</dd></div>
    <div><dt>Path</dt><dd><code>{{.Path}}</code></dd></div>
    <div><dt>Commit</dt><dd><code>{{short .Commit}}</code></dd></div>
  </dl>
  {{if .Findings}}<dl class="detail-list">{{range .Findings}}<div><dt>{{.Code}}</dt><dd>{{.Message}}</dd></div>{{end}}</dl>{{else}}<p class="muted">Validation findings were not available in the bounded projection.</p>{{end}}
</article>
{{end}}
{{define "operator-page"}}<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="htmx-config" content='{"allowEval":false,"allowScriptTags":false,"includeIndicatorStyles":false,"selfRequestsOnly":true}'>
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="/ui/assets/skillet.css">
  <script defer src="/ui/assets/htmx.min.js"></script>
  <script defer src="/ui/assets/skillet.js"></script>
</head>
<body>
  <a class="skip-link" href="#main-content">Skip to content</a>
  <header class="topbar">
    <button class="icon-button nav-toggle" type="button" data-nav-toggle aria-controls="sidebar" aria-expanded="false" aria-label="Open navigation">☰</button>
    <a class="brand" href="/ui/catalogue" aria-label="Skillet home">Skillet</a>
    <div class="topbar-context" aria-label="Current context">
      <span class="context-label">Organisation</span>
      <strong>{{.Identity.Organization}}</strong>
      <span class="context-separator">/</span><span>operator</span>
    </div>
    <div class="session-state" aria-label="Identity and session state">
      <span class="status-dot" aria-hidden="true"></span>
      <span>{{.Identity.Subject}}</span>
      {{if .Identity.Authenticated}}<span class="muted">authenticated</span>{{else}}<span class="muted">development</span>{{end}}
    </div>
  </header>

  <div class="app-shell">
    <aside class="sidebar" id="sidebar" data-sidebar aria-label="Primary navigation">
      <nav class="primary-nav">
        <a href="/ui/catalogue"><span aria-hidden="true">◇</span><span>Capabilities</span></a>
        <a href="/ui/knowledge"><span aria-hidden="true">▤</span><span>Knowledge</span></a>
        <a href="/ui/operator" aria-current="page" class="active"><span aria-hidden="true">⚙</span><span>Operator</span></a>
      </nav>
      <div class="sidebar-note">
        <strong>Bounded administration</strong>
        <p>Operational state is organisation-scoped. Source and startup configuration remain authoritative and read-only here.</p>
      </div>
      <div class="resize-handle" data-sidebar-resize role="separator" aria-orientation="vertical" aria-label="Resize navigation" tabindex="0"></div>
    </aside>
    <button class="drawer-backdrop" type="button" data-nav-close aria-label="Close navigation"></button>

    <main class="main" id="main-content" tabindex="-1">
      <nav class="breadcrumbs" aria-label="Breadcrumb"><a href="/ui/catalogue">Skillet</a><span aria-hidden="true">/</span><span aria-current="page">Operator</span></nav>
      {{if .Error}}
        <section class="state-card error-state" role="alert"><h1>{{.Error}}</h1><p>The operator resource could not be shown or changed in this authorised scope.</p></section>
      {{else}}
        <header class="page-header detail-header">
          <div><span class="eyebrow capability-colour">Operator workspace</span><h1>Operational state and evidence</h1><p>Inspect Skillet-owned operational projections without turning the browser into a second source of truth.</p></div>
        </header>
        {{if .Notice}}<section class="state-card" role="status"><p>{{.Notice}}</p></section>{{end}}

        <div class="detail-grid">
          <section class="detail-section">
            <h2>Identity and authorization</h2>
            <dl class="detail-list">
              <div><dt>Trusted subject</dt><dd>{{.Identity.Subject}}</dd></div>
              <div><dt>Organisation</dt><dd>{{.Identity.Organization}}</dd></div>
              <div><dt>Authorization mode</dt><dd>{{.AuthorizationMode}}</dd></div>
              <div><dt>Capability service</dt><dd>{{if .CapabilityConfigured}}configured{{else}}not configured{{end}}</dd></div>
              <div><dt>Knowledge service</dt><dd>{{if .KnowledgeConfigured}}configured{{else}}not configured{{end}}</dd></div>
            </dl>
            <p class="muted">Raw bearer tokens, provider claims, client secrets, signing keys, and refresh tokens are never rendered.</p>
          </section>
          <section class="detail-section">
            <h2>Runtime health</h2>
            <dl class="detail-list">
              <div><dt>SQLite ready</dt><dd>{{.Health.SQLite}}</dd></div>
              <div><dt>Package store ready</dt><dd>{{.Health.PackageStore}}</dd></div>
              <div><dt>Lexical index ready</dt><dd>{{.Health.LexicalIndex}}</dd></div>
              <div><dt>Initial sync ready</dt><dd>{{.Health.InitialSync}}</dd></div>
              <div><dt>Repository syncs</dt><dd>{{.Health.RepositorySyncs}}</dd></div>
              <div><dt>Sync failures</dt><dd>{{.Health.RepositorySyncFailures}}</dd></div>
              <div><dt>Last successful sync</dt><dd>{{if .Health.LastRepositorySuccess}}{{.Health.LastRepositorySuccess}}{{else}}—{{end}}</dd></div>
              <div><dt>Local audit write failures</dt><dd>{{.Health.AuditWriteFailures}}</dd></div>
            </dl>
          </section>
        </div>

        <section class="detail-section">
          <h2>Source ownership</h2>
          <div class="governance-callout"><strong>Source-owned · read-only</strong><span>Capability definition, owner/maintainers, lifecycle state, replacement guidance, repository URL/ref/trust settings, OKF content, and startup YAML/environment configuration are not editable here.</span></div>
          <p>Change source-authored governance in the owning repository and let normal reconciliation admit a new immutable projection. This UI does not write SKILL.md, OKF, YAML, environment variables, or arbitrary database fields.</p>
        </section>

        <section class="detail-section">
          <h2>Repository and reconciliation status</h2>
          {{if .Snapshot.Repositories}}
            <div class="results-panel">
              {{range .Snapshot.Repositories}}
                <article class="result-card capability-card">
                  <div class="card-heading"><div><span class="badge">source</span><span class="badge">read-only</span></div></div>
                  <h3>{{.ID}}</h3>
                  <dl class="compact-meta">
                    <div><dt>URL</dt><dd>{{.URL}}</dd></div>
                    <div><dt>Tracked ref</dt><dd>{{.Ref}}</dd></div>
                    <div><dt>Trust</dt><dd>{{.TrustLevel}}</dd></div>
                    <div><dt>Owner</dt><dd>{{.Owner}}</dd></div>
                    <div><dt>Active skills</dt><dd>{{.ActiveSkills}}</dd></div>
                    <div><dt>Current quarantined</dt><dd>{{if .CurrentQuarantineKnown}}{{.CurrentQuarantined}}{{else}}unknown{{end}}</dd></div>
                    <div><dt>Historical quarantined revisions</dt><dd>{{.QuarantinedRevisions}}</dd></div>
                    <div><dt>Last reconciliation</dt><dd>{{if .LastReconcileEvent}}{{.LastReconcileEvent}}{{else}}—{{end}}</dd></div>
                    <div><dt>At</dt><dd>{{if .LastReconciledAt}}{{.LastReconciledAt}}{{else}}—{{end}}</dd></div>
                  </dl>
                </article>
              {{end}}
            </div>
          {{else}}<div class="state-card"><p>No repositories are registered in this organisation.</p></div>{{end}}
        </section>

        <section class="detail-section">
          <h2>Current quarantine</h2>
          <p>These are entries from the latest successful source snapshot. Historical immutable revisions are shown separately below.</p>
          {{if .Snapshot.CurrentQuarantined}}
            <div class="results-panel">
              {{range .Snapshot.CurrentQuarantined}}{{template "operator-quarantine-card" .}}{{end}}
            </div>
          {{else}}<div class="state-card"><p>No current quarantined entries were found in the latest successful snapshots. Repositories without a successful snapshot are marked unknown above.</p></div>{{end}}
        </section>

        <section class="detail-section">
          <h2>Quarantine history</h2>
          <p>These are immutable quarantined revisions retained for audit and diagnosis. The list is limited to the 50 most recent revisions.</p>
          {{if .Snapshot.HistoricalQuarantined}}
            <div class="results-panel">
              {{range .Snapshot.HistoricalQuarantined}}{{template "operator-quarantine-card" .}}{{end}}
            </div>
          {{else}}<div class="state-card"><p>No quarantined revisions in this organisation.</p></div>{{end}}
        </section>

        <div class="detail-grid">
          <section class="detail-section">
            <h2>Evidence summary</h2>
            <dl class="detail-list">
              <div><dt>Structured feedback records</dt><dd>{{.Snapshot.FeedbackCount}}</dd></div>
              <div><dt>Lifecycle evidence events</dt><dd>{{.Snapshot.LifecycleCount}}</dd></div>
            </dl>
            <p class="muted">Improvement candidates remain deterministic review artefacts over revision-bound evidence and never change ranking or source.</p>
          </section>
          <section class="detail-section">
            <h2>Audit export health</h2>
            <dl class="detail-list">
              <div><dt>Exporter configured</dt><dd>{{.AuditExport.Enabled}}</dd></div>
              <div><dt>Export attempts</dt><dd>{{.AuditExport.Attempts}}</dd></div>
              <div><dt>Export failures</dt><dd>{{.AuditExport.Failures}}</dd></div>
            </dl>
            {{if .CanAuditProbe}}
              <form method="post" action="/ui/operator/audit-probe">
                <input type="hidden" name="_csrf" value="{{.CSRFToken}}">
                <p>This safe probe records one bounded local audit event and then exercises the configured best-effort exporter. It does not change source, governance, ranking, or runtime execution state.</p>
                <div class="form-actions"><button class="secondary-button" type="submit">Emit audit export probe</button></div>
              </form>
            {{else}}<p class="muted">This identity is not authorised to emit an audit export probe.</p>{{end}}
          </section>
        </div>

        <section class="detail-section">
          <h2>Recent bounded audit events</h2>
          <p>Arbitrary audit detail payloads are intentionally excluded from this view.</p>
          {{if .Snapshot.AuditEvents}}
            <div class="results-panel">
              {{range .Snapshot.AuditEvents}}
                <article class="result-card">
                  <div class="card-heading"><div><span class="badge">audit</span></div><span class="rank">{{.OccurredAt}}</span></div>
                  <h3>{{.EventType}}</h3>
                  <dl class="compact-meta">
                    <div><dt>Actor</dt><dd>{{if .ActorID}}{{.ActorType}} / {{.ActorID}}{{else}}—{{end}}</dd></div>
                    <div><dt>Repository</dt><dd>{{if .RepositoryID}}{{.RepositoryID}}{{else}}—{{end}}</dd></div>
                    <div><dt>Skill</dt><dd>{{if .SkillID}}{{.SkillID}}{{else}}—{{end}}</dd></div>
                    <div><dt>Revision</dt><dd>{{if .RevisionID}}<code>{{short .RevisionID}}</code>{{else}}—{{end}}</dd></div>
                    <div><dt>Request</dt><dd>{{if .RequestID}}<code>{{short .RequestID}}</code>{{else}}—{{end}}</dd></div>
                  </dl>
                </article>
              {{end}}
            </div>
          {{else}}<div class="state-card"><p>No audit events are available in this organisation.</p></div>{{end}}
        </section>
      {{end}}
    </main>
  </div>
</body>
</html>{{end}}
`
