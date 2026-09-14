package httpserver

const webTemplateSource = `{{define "page"}}<!doctype html>
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
<body hx-boost="true">
  <a class="skip-link" href="#main-content">Skip to content</a>
  <header class="topbar">
    <button class="icon-button nav-toggle" type="button" data-nav-toggle aria-controls="sidebar" aria-expanded="false" aria-label="Open navigation">☰</button>
    <a class="brand" href="/ui/catalogue" aria-label="Skillet home">Skillet</a>
    <div class="topbar-context" aria-label="Current context">
      <span class="context-label">Organisation</span>
      <strong>{{.Identity.Organization}}</strong>
      {{if .Scope.Namespace}}<span class="context-separator">/</span><span>{{.Scope.Namespace}}</span>{{end}}
      {{if .Scope.Repository}}<span class="context-separator">/</span><span>{{.Scope.Repository}}</span>{{end}}
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
        <a href="/ui/catalogue" {{if eq .Section "catalogue"}}aria-current="page" class="active"{{end}}>
          <span aria-hidden="true">◇</span><span>Capabilities</span>
        </a>
        <a href="/ui/knowledge" {{if eq .Section "knowledge"}}aria-current="page" class="active"{{end}}>
          <span aria-hidden="true">▤</span><span>Knowledge</span>
        </a>
      </nav>
      <div class="sidebar-note">
        <strong>Read-only browser</strong>
        <p>Discovery and inspection use the same authorised domain state as the agent surfaces.</p>
      </div>
      <div class="resize-handle" data-sidebar-resize role="separator" aria-orientation="vertical" aria-label="Resize navigation" tabindex="0"></div>
    </aside>
    <button class="drawer-backdrop" type="button" data-nav-close aria-label="Close navigation"></button>

    <main class="main" id="main-content" tabindex="-1">
      {{if eq .Section "catalogue"}}
        <nav class="breadcrumbs" aria-label="Breadcrumb"><a href="/ui/catalogue">Capabilities</a>{{if .Capability}}<span aria-hidden="true">/</span><span aria-current="page">{{.Capability.Detail.Descriptor.Name}}</span>{{end}}</nav>
      {{else}}
        <nav class="breadcrumbs" aria-label="Breadcrumb"><a href="/ui/knowledge">Knowledge</a>{{if .Knowledge}}<span aria-hidden="true">/</span><span aria-current="page">{{if .Knowledge.Read.Metadata.Title}}{{.Knowledge.Read.Metadata.Title}}{{else}}{{.Knowledge.Read.Chunk.Path}}{{end}}</span>{{end}}</nav>
      {{end}}

      {{if .Error}}
        <section class="state-card error-state" role="alert">
          <h1>{{.Error}}</h1>
          <p>The requested resource could not be shown in this authorised scope.</p>
        </section>
      {{else if .Capability}}
        {{template "capability-detail" .Capability}}
      {{else if .Knowledge}}
        {{template "knowledge-read" .Knowledge}}
      {{else if eq .Section "knowledge"}}
        {{template "knowledge-search" .}}
      {{else}}
        {{template "catalogue-search" .}}
      {{end}}
    </main>
  </div>
</body>
</html>{{end}}

{{define "catalogue-search"}}
<header class="page-header">
  <div><span class="eyebrow capability-colour">Capability catalogue</span><h1>Find reusable capabilities</h1><p>Search uses Skillet's scoped capability service. View filters refine the authorised ranked window without introducing new ranking semantics.</p></div>
</header>
<form class="search-panel" method="get" action="/ui/catalogue" hx-get="/ui/catalogue" hx-target="#catalogue-panel" hx-select="#catalogue-panel" hx-push-url="true" hx-indicator="#catalogue-loading">
  <label class="search-field"><span>Search</span><input type="search" name="q" value="{{.Query}}" placeholder="e.g. incident response" autocomplete="off"></label>
  <div class="filter-grid">
    <label><span>Namespace</span><input name="namespace" value="{{.Scope.Namespace}}" placeholder="optional"></label>
    <label><span>Repository</span><input name="repository" value="{{.Scope.Repository}}" placeholder="optional"></label>
    <label><span>Kind</span><select name="kind"><option value="">Any</option><option value="skill" {{if eq .Filters.Kind "skill"}}selected{{end}}>Skill</option><option value="playbook" {{if eq .Filters.Kind "playbook"}}selected{{end}}>Playbook</option><option value="tool" {{if eq .Filters.Kind "tool"}}selected{{end}}>Tool metadata</option></select></label>
    <label><span>Governance</span><select name="state"><option value="">Any</option><option value="active" {{if eq .Filters.State "active"}}selected{{end}}>Active</option><option value="deprecated" {{if eq .Filters.State "deprecated"}}selected{{end}}>Deprecated</option><option value="yanked" {{if eq .Filters.State "yanked"}}selected{{end}}>Yanked</option></select></label>
    <label><span>Owner</span><input name="owner" value="{{.Filters.Owner}}" placeholder="where configured"></label>
    <label><span>Trust</span><input name="trust" value="{{.Filters.Trust}}" placeholder="e.g. approved"></label>
  </div>
  <div class="form-actions"><button class="primary-button" type="submit">Search</button><span id="catalogue-loading" class="htmx-indicator loading-state" role="status" aria-live="polite">Loading…</span></div>
</form>
<section id="catalogue-panel" class="results-panel" aria-live="polite">
  {{if .Notice}}<div class="state-card"><p>{{.Notice}}</p></div>{{end}}
  {{range .Capabilities}}
    <article class="result-card capability-card">
      <div class="card-heading"><div><span class="badge kind-badge">{{.Capability.Identity.Kind}}</span><span class="badge state-{{.Capability.Status}}">{{.Capability.Status}}</span></div></div>
      <h2><a href="{{.DetailURL}}">{{.Capability.Name}}</a></h2>
      <p>{{.Capability.Description}}</p>
      <dl class="compact-meta"><div><dt>Version</dt><dd>{{if .Capability.Version}}{{.Capability.Version}}{{else}}—{{end}}</dd></div><div><dt>Owner</dt><dd>{{if .Capability.Governance.Owner}}{{.Capability.Governance.Owner}}{{else}}—{{end}}</dd></div><div><dt>Trust</dt><dd>{{.Capability.TrustLevel}}</dd></div><div><dt>Source</dt><dd>{{.Capability.Source.RepositoryID}}</dd></div></dl>
    </article>
  {{end}}
</section>
{{end}}

{{define "capability-detail"}}
{{$d := .Detail.Descriptor}}
<header class="page-header detail-header">
  <div><div class="badge-row"><span class="badge kind-badge">{{$d.Identity.Kind}}</span><span class="badge state-{{$d.Status}}">{{$d.Status}}</span>{{if $d.TrustLevel}}<span class="badge">{{$d.TrustLevel}}</span>{{end}}</div><h1>{{$d.Name}}</h1><p>{{$d.Description}}</p></div>
</header>
{{if ne $d.Status "active"}}<section class="governance-callout" aria-label="Governance status"><strong>{{$d.Status}}</strong>{{if $d.Governance.Reason}}<span>{{$d.Governance.Reason}}</span>{{end}}{{if $d.Governance.ReplacedBy}}<span>Replacement: <code>{{$d.Governance.ReplacedBy}}</code> (guidance only; not followed automatically)</span>{{end}}</section>{{end}}
<div class="detail-grid">
  <section class="detail-section"><h2>Identity and provenance</h2><dl class="detail-list"><div><dt>Stable identity</dt><dd><code>{{$d.Identity.ID}}</code></dd></div><div><dt>Revision</dt><dd><code>{{$d.Provenance.RevisionID}}</code></dd></div><div><dt>Version</dt><dd>{{if $d.Version}}{{$d.Version}}{{else}}—{{end}}</dd></div><div><dt>Commit</dt><dd><code>{{short $d.Provenance.Commit}}</code></dd></div><div><dt>Tree</dt><dd><code>{{short $d.Provenance.Tree}}</code></dd></div><div><dt>Scope</dt><dd>{{$d.Scope.Organization}}{{if $d.Scope.Namespace}} / {{$d.Scope.Namespace}}{{end}}{{if $d.Scope.Repository}} / {{$d.Scope.Repository}}{{end}}</dd></div></dl></section>
  <section class="detail-section"><h2>Ownership and compatibility</h2><dl class="detail-list"><div><dt>Owner</dt><dd>{{if $d.Governance.Owner}}{{$d.Governance.Owner}}{{else}}—{{end}}</dd></div><div><dt>Maintainers</dt><dd>{{if $d.Governance.Maintainers}}{{join $d.Governance.Maintainers ", "}}{{else}}—{{end}}</dd></div><div><dt>Compatibility</dt><dd>{{if $d.Compatibility}}{{$d.Compatibility}}{{else}}—{{end}}</dd></div><div><dt>Trust</dt><dd>{{$d.TrustLevel}}</dd></div><div><dt>Visibility</dt><dd>{{$d.Governance.Visibility}}</dd></div></dl></section>
</div>
<section class="detail-section"><h2>Source</h2><dl class="detail-list"><div><dt>Repository</dt><dd>{{$d.Source.RepositoryID}}</dd></div><div><dt>Path</dt><dd><code>{{$d.Source.Path}}</code></dd></div><div><dt>Reference</dt><dd>{{if .SourceURL}}<a href="{{.SourceURL}}" rel="noreferrer">{{.SourceURL}}</a>{{else}}{{$d.Source.URL}}{{end}}</dd></div></dl></section>
{{if eq $d.Identity.Kind "skill"}}
<section class="detail-section"><h2>Materialisation</h2><p>This browser never executes capabilities. Materialisation remains an explicit client action through <code>{{.Detail.MaterializeWith}}</code>.</p>{{if .MaterializeCandidateID}}<div class="copy-row"><code id="materialize-command">materialize_skill candidate_id={{.MaterializeCandidateID}}</code><button type="button" class="secondary-button" data-copy-target="materialize-command">Copy</button></div>{{else}}<p class="muted">Search and explicitly select this revision to obtain a short-lived signed candidate before materialising it.</p>{{end}}{{if .Detail.PackageDigests.TarGZ}}<dl class="detail-list"><div><dt>tar.gz SHA-256</dt><dd><code>{{.Detail.PackageDigests.TarGZ}}</code></dd></div>{{if .Detail.PackageDigests.ZIP}}<div><dt>zip SHA-256</dt><dd><code>{{.Detail.PackageDigests.ZIP}}</code></dd></div>{{end}}</dl>{{end}}</section>
{{end}}
{{with .Detail.Tool}}<section class="detail-section tool-warning"><h2>Tool metadata</h2><p><strong>Non-executable.</strong> Skillet exposes this MCP tool only as untrusted catalogue metadata.</p><dl class="detail-list"><div><dt>Server</dt><dd>{{.ServerID}}</dd></div><div><dt>Tool</dt><dd>{{.Name}}</dd></div><div><dt>Schema summary</dt><dd>{{.InputSchemaSummary}}</dd></div><div><dt>Schema digest</dt><dd><code>{{.InputSchemaSHA256}}</code></dd></div></dl></section>{{end}}
{{if .Detail.Descriptor.Metadata}}<section class="detail-section"><h2>Metadata</h2><dl class="detail-list">{{range metadata .Detail.Descriptor.Metadata}}<div><dt>{{index . 0}}</dt><dd>{{index . 1}}</dd></div>{{end}}</dl></section>{{end}}
<section class="detail-section"><h2>Evidence summary</h2>{{if .Evidence}}<dl class="detail-list"><div><dt>Reviewable candidates</dt><dd>{{.Evidence.Total}}</dd></div><div><dt>Feedback evidence</dt><dd>{{.Evidence.FeedbackEvidenceIncluded}}</dd></div><div><dt>Lifecycle evidence</dt><dd>{{.Evidence.LifecycleEvidenceIncluded}}</dd></div><div><dt>Source truncated</dt><dd>{{.Evidence.SourceEvidenceTruncated}}</dd></div></dl>{{else if .EvidenceDenied}}<p class="muted">Evidence review is not authorised for this identity.</p>{{else}}<p class="muted">No bounded evidence summary is available for this capability.</p>{{end}}</section>
{{end}}

{{define "knowledge-search"}}
<header class="page-header"><div><span class="eyebrow knowledge-colour">Knowledge</span><h1>Search organisational knowledge</h1><p>Results are compact source-backed chunks. Knowledge remains distinct from executable/reusable capabilities.</p></div></header>
<form class="search-panel knowledge-search" method="get" action="/ui/knowledge" hx-get="/ui/knowledge" hx-target="#knowledge-panel" hx-select="#knowledge-panel" hx-push-url="true" hx-indicator="#knowledge-loading"><label class="search-field"><span>Information need</span><input type="search" name="q" value="{{.Query}}" placeholder="e.g. renewal objection policy" autocomplete="off"></label><div class="form-actions"><button class="primary-button knowledge-button" type="submit">Search knowledge</button><span id="knowledge-loading" class="htmx-indicator loading-state" role="status" aria-live="polite">Loading…</span></div></form>
<section id="knowledge-panel" class="results-panel" aria-live="polite">{{if .Notice}}<div class="state-card"><p>{{.Notice}}</p></div>{{end}}{{range .KnowledgeResults}}<article class="result-card knowledge-card"><div class="card-heading"><div><span class="badge knowledge-badge">knowledge</span>{{if .Result.Metadata.Status}}<span class="badge">{{.Result.Metadata.Status}}</span>{{end}}</div><span class="rank">rank {{.Result.Result.Rank}}</span></div><h2><a href="{{.ReadURL}}">{{if .Result.Metadata.Title}}{{.Result.Metadata.Title}}{{else if .Result.Result.Heading}}{{.Result.Result.Heading}}{{else}}{{.Result.Result.Path}}{{end}}</a></h2><p>{{.Result.Result.Snippet}}</p><dl class="compact-meta"><div><dt>Path</dt><dd>{{.Result.Result.Path}}</dd></div><div><dt>Source revision</dt><dd><code>{{short .Result.Result.SourceRevision}}</code></dd></div><div><dt>Type</dt><dd>{{if .Result.Metadata.Type}}{{.Result.Metadata.Type}}{{else}}Markdown{{end}}</dd></div></dl></article>{{end}}</section>
{{end}}

{{define "knowledge-read"}}
<header class="page-header detail-header"><div><div class="badge-row"><span class="badge knowledge-badge">knowledge</span>{{if .Read.Metadata.Type}}<span class="badge">{{.Read.Metadata.Type}}</span>{{end}}{{if .Read.Metadata.Status}}<span class="badge">{{.Read.Metadata.Status}}</span>{{end}}</div><h1>{{if .Read.Metadata.Title}}{{.Read.Metadata.Title}}{{else if .Read.Chunk.Heading}}{{.Read.Chunk.Heading}}{{else}}{{.Read.Chunk.Path}}{{end}}</h1>{{if .Read.Metadata.Description}}<p>{{.Read.Metadata.Description}}</p>{{end}}</div></header>
<div class="detail-grid"><section class="detail-section"><h2>Provenance</h2><dl class="detail-list"><div><dt>Document</dt><dd><code>{{short .Read.Chunk.DocumentID}}</code></dd></div><div><dt>Chunk</dt><dd><code>{{short .Read.Chunk.ID}}</code></dd></div><div><dt>Path</dt><dd>{{.Read.Chunk.Path}}</dd></div><div><dt>Source revision</dt><dd><code>{{.Read.Chunk.SourceRevision}}</code></dd></div><div><dt>Source</dt><dd>{{if .SourceURL}}<a href="{{.SourceURL}}" rel="noreferrer">{{.SourceURL}}</a>{{else}}{{.SourceText}}{{end}}</dd></div></dl></section><section class="detail-section"><h2>Freshness</h2><dl class="detail-list"><div><dt>Status</dt><dd>{{if .Read.Metadata.Status}}{{.Read.Metadata.Status}}{{else}}—{{end}}</dd></div><div><dt>Timestamp</dt><dd>{{if .Read.Metadata.Timestamp}}{{.Read.Metadata.Timestamp}}{{else}}—{{end}}</dd></div><div><dt>Stale after</dt><dd>{{if .Read.Metadata.StaleAfter}}{{.Read.Metadata.StaleAfter}}{{else}}—{{end}}</dd></div>{{with .Read.Metadata.Generated}}<div><dt>Generated by</dt><dd>{{.By}}{{if .At}} · {{.At}}{{end}}</dd></div>{{end}}</dl></section></div>
<article class="knowledge-body" aria-label="Knowledge content"><pre>{{.Read.Chunk.Content}}</pre></article>
{{if .Read.Links}}<section class="detail-section"><h2>Explicit outgoing links</h2><ul class="link-list">{{range .Read.Links}}<li><span>{{if .Text}}{{.Text}}{{else}}{{.TargetPath}}{{end}}</span><code>{{.TargetPath}}</code>{{if not .Resolved}}<span class="badge">unresolved</span>{{end}}</li>{{end}}</ul></section>{{end}}
<section class="detail-section"><h2>Backlinks</h2>{{if .Backlinks}}<ul class="link-list">{{range .Backlinks}}<li><strong>{{if .Title}}{{.Title}}{{else}}{{.Path}}{{end}}</strong><span>{{.LinkText}}</span><code>{{.Path}}</code></li>{{end}}</ul>{{else}}<p class="muted">No authorised explicit backlinks.</p>{{end}}</section>
{{end}}
`