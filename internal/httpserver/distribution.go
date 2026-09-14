package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"unicode"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/composition"
	"github.com/mhingston/skillet/internal/distribution"
	"github.com/mhingston/skillet/internal/search"
)

type distributionWebPage struct {
	Title              string
	Identity           webIdentity
	Scope              webScope
	Collections        []composition.Collection
	SelectedCollection string
	Artifact           *distribution.Artifact
	ManifestURL        string
	Error              string
}

func (s *Server) addDistributionRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /ui/distribution", s.webDistribution)
	mux.HandleFunc("GET /v1/distribution/claude-code/marketplace.json", s.claudeCodeMarketplace)
}

func (s *Server) webDistribution(w http.ResponseWriter, r *http.Request) {
	base := s.baseWebPage(r, "distribution", "Distribution")
	page := distributionWebPage{
		Title: "Distribution · Skillet",
		Identity: base.Identity,
		Scope: scopeFromRequest(r),
		SelectedCollection: strings.TrimSpace(r.URL.Query().Get("collection")),
	}
	requestScope, err := s.distributionScope(r.Context(), page.Scope)
	if err != nil {
		s.renderDistributionError(w, r, page, http.StatusBadRequest, err)
		return
	}
	if err := s.authorize(r.Context(), authz.ActionCapabilitySearch, authz.Resource{
		OrganizationID: requestScope.Organization, Namespace: requestScope.Namespace, Repository: requestScope.Repository,
	}); err != nil {
		s.renderDistributionError(w, r, page, http.StatusForbidden, err)
		return
	}
	page.Collections = s.authorizedDistributionCollections(r.Context(), requestScope)
	artifact, err := s.buildDistributionArtifact(r.Context(), requestScope, page.SelectedCollection)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAuthorizationDenied) { status = http.StatusForbidden }
		if errors.Is(err, errDistributionUnavailable) { status = http.StatusServiceUnavailable }
		s.renderDistributionError(w, r, page, status, err)
		return
	}
	page.Artifact = &artifact
	values := url.Values{}
	if page.Scope.Namespace != "" { values.Set("namespace", page.Scope.Namespace) }
	if page.Scope.Repository != "" { values.Set("repository", page.Scope.Repository) }
	if page.SelectedCollection != "" { values.Set("collection", page.SelectedCollection) }
	page.ManifestURL = "/v1/distribution/claude-code/marketplace.json"
	if encoded := values.Encode(); encoded != "" { page.ManifestURL += "?" + encoded }
	s.renderDistributionPage(w, r, http.StatusOK, page)
}

func (s *Server) claudeCodeMarketplace(w http.ResponseWriter, r *http.Request) {
	requestScope, err := s.distributionScope(r.Context(), scopeFromRequest(r))
	if err != nil {
		http.Error(w, "invalid distribution scope", http.StatusBadRequest)
		return
	}
	if err := s.authorize(r.Context(), authz.ActionCapabilitySearch, authz.Resource{
		OrganizationID: requestScope.Organization, Namespace: requestScope.Namespace, Repository: requestScope.Repository,
	}); err != nil {
		http.Error(w, "distribution is not authorized", http.StatusForbidden)
		return
	}
	artifact, err := s.buildDistributionArtifact(r.Context(), requestScope, strings.TrimSpace(r.URL.Query().Get("collection")))
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAuthorizationDenied) { status = http.StatusForbidden }
		if errors.Is(err, errDistributionUnavailable) { status = http.StatusServiceUnavailable }
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="marketplace.json"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("ETag", `"sha256-`+artifact.ManifestSHA256+`"`)
	w.Header().Set("X-Skillet-Distribution-Profile", artifact.Profile)
	w.Header().Set("X-Skillet-Manifest-SHA256", artifact.ManifestSHA256)
	_, _ = w.Write(artifact.Manifest)
}

var errDistributionUnavailable = errors.New("distribution is unavailable")

func (s *Server) distributionScope(ctx context.Context, requested webScope) (capability.Scope, error) {
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok { organizationID = authenticated }
	return capability.NewScope(organizationID, requested.Namespace, requested.Repository)
}

func (s *Server) buildDistributionArtifact(ctx context.Context, requestScope capability.Scope, collectionID string) (distribution.Artifact, error) {
	service := capabilityServiceFor(s)
	if service == nil || s.catalogue == nil {
		return distribution.Artifact{}, errDistributionUnavailable
	}
	if err := s.authorize(ctx, authz.ActionCapabilitySearch, authz.Resource{
		OrganizationID: requestScope.Organization, Namespace: requestScope.Namespace, Repository: requestScope.Repository,
	}); err != nil {
		return distribution.Artifact{}, err
	}

	collectionID = strings.TrimSpace(collectionID)
	items := []distribution.Capability{}
	preWarnings := []distribution.Warning{}
	plannedCount := -1
	if collectionID != "" {
		plan, err := s.resolveCapabilityPlan(ctx, resolveCapabilityPlanInput{
			Collection: collectionID,
			Scope: capabilityScopeInput{Namespace: requestScope.Namespace, Repository: requestScope.Repository},
		})
		if err != nil { return distribution.Artifact{}, fmt.Errorf("collection snapshot is unavailable") }
		plannedCount = len(plan.Capabilities)
		for _, locked := range plan.Capabilities {
			if locked.Kind != capability.KindSkill {
				return distribution.Artifact{}, fmt.Errorf("collection snapshot cannot be represented by the Claude Code skill marketplace profile")
			}
			item, ok, err := s.distributionCapability(ctx, service, requestScope, locked.ID, locked.RevisionID, locked.Status)
			if err != nil { return distribution.Artifact{}, err }
			if !ok {
				return distribution.Artifact{}, fmt.Errorf("collection snapshot cannot be represented by the Claude Code skill marketplace profile")
			}
			items = append(items, item)
		}
	} else {
		docs, err := service.List(requestScope, search.Filters{})
		if err != nil { return distribution.Artifact{}, err }
		if len(docs) > distribution.MaxCapabilitiesPerArtifact {
			return distribution.Artifact{}, fmt.Errorf("authorized distribution snapshot exceeds profile limit")
		}
		for _, doc := range docs {
			descriptor, err := service.Describe(doc.ID, requestScope)
			if err != nil { continue }
			if descriptor.Identity.Kind != capability.KindSkill { continue }
			if descriptor.TrustLevel != "" && descriptor.TrustLevel != "approved" { continue }
			if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{
				OrganizationID: descriptor.Scope.Organization, Namespace: descriptor.Scope.Namespace,
				Repository: descriptor.Scope.Repository, ID: descriptor.Identity.ID,
			}); err != nil {
				continue
			}
			item, ok, err := s.distributionCapability(ctx, service, requestScope, descriptor.Identity.ID, descriptor.Provenance.RevisionID, descriptor.Status)
			if err != nil { return distribution.Artifact{}, err }
			if !ok {
				preWarnings = append(preWarnings, distribution.Warning{
					CapabilityID: descriptor.Identity.ID, Code: "source_unavailable",
					Message: "authorized capability was omitted because immutable source provenance is unavailable",
				})
				continue
			}
			items = append(items, item)
		}
	}

	artifact, err := distribution.BuildClaudeCodeMarketplace(distributionMarketplaceName(requestScope, collectionID), items)
	if err != nil { return distribution.Artifact{}, err }
	artifact.Warnings = append(preWarnings, artifact.Warnings...)
	if plannedCount >= 0 && len(artifact.Entries) != plannedCount {
		return distribution.Artifact{}, fmt.Errorf("collection snapshot cannot be represented by the Claude Code skill marketplace profile")
	}
	return artifact, nil
}

func (s *Server) distributionCapability(ctx context.Context, service *capability.Service, requestScope capability.Scope, identityID, revisionID string, status capability.Status) (distribution.Capability, bool, error) {
	authoritative, ok := service.ScopeForIdentity(identityID)
	if !ok || authoritative.Organization != requestScope.Organization || !authoritative.Allows(requestScope) {
		return distribution.Capability{}, false, nil
	}
	if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{
		OrganizationID: authoritative.Organization, Namespace: authoritative.Namespace,
		Repository: authoritative.Repository, ID: identityID,
	}); err != nil {
		return distribution.Capability{}, false, nil
	}
	revisions, err := s.catalogue.CompositionRevisions(ctx, requestScope.Organization, identityID)
	if err != nil { return distribution.Capability{}, false, err }
	for _, revision := range revisions {
		if revision.RevisionID != revisionID { continue }
		if revision.TrustLevel != "" && revision.TrustLevel != "approved" { return distribution.Capability{}, false, nil }
		return distribution.Capability{
			ID: revision.SkillID, Name: revision.Name, Description: revision.Description, Version: revision.Version,
			Status: status, RepositoryURL: revision.RepositoryURL, Path: revision.Path,
			RevisionID: revision.RevisionID, Commit: revision.Commit, Tree: revision.Tree,
			ArchiveSHA256TarGZ: revision.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: revision.ArchiveSHA256ZIP,
		}, true, nil
	}
	return distribution.Capability{}, false, nil
}

func (s *Server) authorizedDistributionCollections(ctx context.Context, requestScope capability.Scope) []composition.Collection {
	collections, err := compositionCollectionsFor(s)
	if err != nil { return nil }
	out := make([]composition.Collection, 0, len(collections))
	for _, collection := range collections {
		artifact, buildErr := s.buildDistributionArtifact(ctx, requestScope, collection.ID)
		if buildErr == nil && len(artifact.Entries) > 0 {
			out = append(out, collection)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func distributionMarketplaceName(scope capability.Scope, collectionID string) string {
	raw := scope.Organization + "\x00" + scope.Namespace + "\x00" + scope.Repository + "\x00" + collectionID
	sum := sha256.Sum256([]byte(raw))
	label := distributionSlug(scope.Organization)
	if collectionID != "" { label += "-" + distributionSlug(collectionID) }
	label = strings.Trim(label, "-")
	if label == "" { label = "catalogue" }
	if len(label) > 40 { label = strings.Trim(label[:40], "-") }
	return "skillet-" + label + "-" + hex.EncodeToString(sum[:4])
}

func distributionSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	dash := false
	for _, r := range value {
		if (unicode.IsLetter(r) || unicode.IsDigit(r)) && r <= unicode.MaxASCII {
			b.WriteRune(r); dash = false; continue
		}
		if b.Len() > 0 && !dash { b.WriteByte('-'); dash = true }
	}
	return strings.Trim(b.String(), "-")
}

func (s *Server) renderDistributionPage(w http.ResponseWriter, r *http.Request, status int, page distributionWebPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := distributionTemplate.ExecuteTemplate(w, "distribution-page", page); err != nil && s.log != nil {
		s.log.Error("render distribution page", "error", err)
	}
}

func (s *Server) renderDistributionError(w http.ResponseWriter, r *http.Request, page distributionWebPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && !errors.Is(err, ErrAuthorizationDenied) { page.Error = err.Error() }
	s.renderDistributionPage(w, r, status, page)
}

var distributionTemplate = template.Must(template.New("distribution-page").Funcs(template.FuncMap{
	"short": shortValue,
}).Parse(distributionTemplateSource))

const distributionTemplateSource = `{{define "distribution-page"}}<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/ui/assets/skillet.css"></head>
<body><a class="skip-link" href="#main-content">Skip to content</a><header class="topbar"><a class="brand" href="/ui/catalogue">Skillet</a><div class="topbar-context"><strong>{{.Identity.Organization}}</strong>{{if .Scope.Namespace}} / {{.Scope.Namespace}}{{end}}{{if .Scope.Repository}} / {{.Scope.Repository}}{{end}}</div><div class="session-state"><span>{{.Identity.Subject}}</span></div></header>
<div class="app-shell"><aside class="sidebar"><nav class="primary-nav"><a href="/ui/catalogue">◇ Capabilities</a><a href="/ui/knowledge">▤ Knowledge</a><a class="active" aria-current="page" href="/ui/distribution">⇩ Distribution</a><a href="/ui/operator">⚙ Operator</a></nav><div class="sidebar-note"><strong>Read-only export</strong><p>The adapter projects authorized immutable state. It does not install or execute capabilities.</p></div></aside>
<main class="main" id="main-content">{{if .Error}}<section class="state-card error-state" role="alert"><h1>{{.Error}}</h1><p>The distribution snapshot could not be produced in this authorised scope.</p></section>{{else}}
<header class="page-header"><div><span class="eyebrow capability-colour">Host-native distribution</span><h1>Claude Code marketplace export</h1><p>Verified against Claude Code {{.Artifact.Profile}} contract (CLI {{` + "`" + `2.1.270` + "`" + `}}). Sources are read-only Git subdirectories pinned to exact commits; Skillet never delegates repository credentials.</p></div></header>
<form class="search-panel" method="get" action="/ui/distribution"><div class="filter-grid"><label><span>Namespace</span><input name="namespace" value="{{.Scope.Namespace}}" placeholder="optional"></label><label><span>Repository</span><input name="repository" value="{{.Scope.Repository}}" placeholder="optional"></label><label><span>Collection</span><select name="collection"><option value="">All authorised current skills</option>{{range .Collections}}<option value="{{.ID}}" {{if eq $.SelectedCollection .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></label></div><div class="form-actions"><button class="primary-button" type="submit">Build snapshot</button></div></form>
<section class="governance-callout"><strong>Snapshot boundary</strong><span>Only capabilities visible and authorised for this request are considered. Yanked capabilities are excluded without naming them. Deprecated capabilities remain explicit and are never silently substituted.</span></section>
<section class="detail-section"><h2>Export and install</h2><p>Download the authorised manifest, save it as <code>skillet-marketplace/.claude-plugin/marketplace.json</code>, then add that local directory. The exported file contains no Skillet token or source-repository credential.</p><div class="copy-row"><a class="primary-button" href="{{.ManifestURL}}" download="marketplace.json">Download marketplace.json</a></div><pre><code>claude plugin marketplace add ./skillet-marketplace</code></pre><dl class="detail-list"><div><dt>Marketplace</dt><dd><code>{{.Artifact.MarketplaceName}}</code></dd></div><div><dt>Manifest SHA-256</dt><dd><code>{{.Artifact.ManifestSHA256}}</code></dd></div><div><dt>Profile</dt><dd>{{.Artifact.Profile}}</dd></div></dl></section>
<section class="detail-section"><h2>Included capabilities</h2>{{if .Artifact.Entries}}<div class="results-panel">{{range .Artifact.Entries}}<article class="result-card capability-card"><div class="badge-row"><span class="badge state-{{.Status}}">{{.Status}}</span></div><h3>{{.CapabilityName}}</h3><pre><code>claude plugin install {{.PluginName}}@{{$.Artifact.MarketplaceName}}</code></pre><dl class="compact-meta"><div><dt>Version</dt><dd>{{if .CapabilityVersion}}{{.CapabilityVersion}}{{else}}—{{end}}</dd></div><div><dt>Revision</dt><dd><code>{{short .RevisionID}}</code></dd></div><div><dt>Commit</dt><dd><code>{{short .Commit}}</code></dd></div><div><dt>tar.gz SHA-256</dt><dd><code>{{if .ArchiveSHA256TarGZ}}{{short .ArchiveSHA256TarGZ}}{{else}}—{{end}}</code></dd></div><div><dt>Source</dt><dd><code>{{.Path}}</code></dd></div></dl></article>{{end}}</div>{{else}}<div class="state-card"><p>No authorised skills in this scope can be represented by the current profile.</p></div>{{end}}</section>
{{if .Artifact.Warnings}}<section class="detail-section tool-warning"><h2>Warnings</h2><ul>{{range .Artifact.Warnings}}<li>{{.Message}}</li>{{end}}</ul></section>{{end}}
<section class="detail-section"><h2>Host contract</h2><p>This M3 profile emits <code>.claude-plugin/marketplace.json</code> data with <code>strict: false</code>, one skill per plugin, and <code>git-subdir</code> sources pinned by <code>sha</code>. The marketplace snapshot is installation metadata only: no hooks, MCP servers, workflow orchestration, runtime SDK, or execution surface is generated.</p></section>
{{end}}</main></div></body></html>{{end}}`
