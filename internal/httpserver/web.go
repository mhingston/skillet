package httpserver

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/a-h/templ"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/knowledge"
)

//go:embed webassets/*
var webAssets embed.FS

const browserCSP = "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; object-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'"

type webIdentity struct {
	Subject       string
	Organization  string
	Authenticated bool
}

type webScope struct {
	Namespace  string
	Repository string
}

type webFilters struct {
	Kind  string
	State string
	Owner string
	Trust string
}

type webCapabilityItem struct {
	CandidateID string
	Capability  capability.Descriptor
	DetailURL   string
}

type webCapabilityView struct {
	Detail                 capability.Detail
	MaterializeCandidateID string
	Evidence                *improvementCandidatesOutput
	EvidenceDenied          bool
	SourceURL               string
}

type webKnowledgeItem struct {
	Result  knowledge.OKFSearchResult
	ReadURL string
}

type webKnowledgeView struct {
	Read       knowledge.OKFRead
	Backlinks  []knowledge.Backlink
	SourceURL  string
	SourceText string
}

type webPageData struct {
	Title            string
	Section          string
	Identity         webIdentity
	OperatorVisible  bool
	Scope            webScope
	Query            string
	Filters          webFilters
	Notice           string
	Error            string
	Capabilities     []webCapabilityItem
	Capability       *webCapabilityView
	KnowledgeResults []webKnowledgeItem
	Knowledge        *webKnowledgeView
}

func (s *Server) addWebRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	assets, err := fs.Sub(webAssets, "webassets")
	if err != nil {
		panic(fmt.Sprintf("web assets: %v", err))
	}
	mux.Handle("GET /ui/assets/{name}", s.webAssetHandler(assets))
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/catalogue", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /ui/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/catalogue", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /ui/catalogue", s.webCatalogue)
	mux.HandleFunc("GET /ui/catalogue/{revisionID}", s.webCapabilityDetail)
	mux.HandleFunc("GET /ui/knowledge", s.webKnowledgeSearch)
	mux.HandleFunc("GET /ui/knowledge/{chunkID}", s.webKnowledgeRead)
	s.addOperatorRoutes(mux)
}

func (s *Server) webAssetHandler(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Base(strings.TrimSpace(r.PathValue("name")))
		if name == "." || name == "" || name != r.PathValue("name") {
			http.NotFound(w, r)
			return
		}
		contents, err := fs.ReadFile(assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setBrowserSecurityHeaders(w)
		w.Header().Set("Cache-Control", "public, max-age=3600")
		switch path.Ext(name) {
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		case ".js":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		_, _ = w.Write(contents)
	})
}

func (s *Server) webCatalogue(w http.ResponseWriter, r *http.Request) {
	page := s.baseWebPage(r, "catalogue", "Capability catalogue")
	page.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	page.Scope = scopeFromRequest(r)
	page.Filters = filtersFromRequest(r)
	service := capabilityServiceFor(s)
	if service == nil {
		page.Notice = "Capability catalogue is not configured."
		s.renderWebPage(w, r, http.StatusOK, page)
		return
	}
	if page.Query == "" {
		page.Notice = "Search authorised capabilities by task or intent."
		s.renderWebPage(w, r, http.StatusOK, page)
		return
	}
	input := searchCapabilitiesInput{Query: page.Query, Scope: capabilityScopeInput{Namespace: page.Scope.Namespace, Repository: page.Scope.Repository}, Limit: 10}
	if page.Filters.Trust != "" {
		input.Filters.TrustLevels = []string{page.Filters.Trust}
	}
	_, output, err := s.searchCapabilitiesTool(r.Context(), service, nil, input)
	if err != nil {
		s.renderWebError(w, r, page, err)
		return
	}
	for _, candidate := range output.Candidates {
		if !webCapabilityMatches(candidate.Capability, page.Filters) {
			continue
		}
		values := url.Values{}
		values.Set("candidate_id", candidate.CandidateID)
		if page.Scope.Namespace != "" {
			values.Set("namespace", page.Scope.Namespace)
		}
		if page.Scope.Repository != "" {
			values.Set("repository", page.Scope.Repository)
		}
		page.Capabilities = append(page.Capabilities, webCapabilityItem{
			CandidateID: candidate.CandidateID,
			Capability:  candidate.Capability,
			DetailURL:   "/ui/catalogue/" + url.PathEscape(candidate.Capability.Provenance.RevisionID) + "?" + values.Encode(),
		})
	}
	if len(page.Capabilities) == 0 {
		page.Notice = "No authorised capabilities matched this query and view filter."
	}
	s.renderWebPage(w, r, http.StatusOK, page)
}

func (s *Server) webCapabilityDetail(w http.ResponseWriter, r *http.Request) {
	page := s.baseWebPage(r, "catalogue", "Capability detail")
	page.Scope = scopeFromRequest(r)
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	service := capabilityServiceFor(s)
	if service == nil || revisionID == "" {
		s.renderWebNotFound(w, r, page)
		return
	}
	var detail capability.Detail
	var materializeCandidateID string
	candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
	if candidateID != "" {
		_, output, err := s.describeCapabilityTool(r.Context(), service, nil, describeCapabilityInput{CandidateID: candidateID, Scope: capabilityScopeInput{Namespace: page.Scope.Namespace, Repository: page.Scope.Repository}})
		if err != nil {
			s.renderWebError(w, r, page, err)
			return
		}
		if output.Detail.Descriptor.Provenance.RevisionID != revisionID {
			s.renderWebNotFound(w, r, page)
			return
		}
		detail = output.Detail
		materializeCandidateID = output.MaterializeCandidateID
	} else {
		resolved, status, err := s.webDescribeCapabilityRevision(r.Context(), service, revisionID, page.Scope)
		if err != nil {
			if status == http.StatusNotFound {
				s.renderWebNotFound(w, r, page)
				return
			}
			s.renderWebErrorStatus(w, r, page, err, status)
			return
		}
		detail = resolved
	}
	view := &webCapabilityView{Detail: detail, MaterializeCandidateID: materializeCandidateID, SourceURL: safeHTTPURL(detail.Descriptor.Source.URL)}
	if s.catalogue != nil {
		_, summary, err := s.improvementCandidatesTool(r.Context(), nil, improvementCandidatesInput{SkillID: detail.Descriptor.Identity.ID, RevisionID: revisionID, Limit: 5})
		switch {
		case err == nil:
			view.Evidence = &summary
		case errors.Is(err, ErrAuthorizationDenied):
			view.EvidenceDenied = true
		}
	}
	page.Title = detail.Descriptor.Name + " · Skillet"
	page.Capability = view
	s.renderWebPage(w, r, http.StatusOK, page)
}

func (s *Server) webDescribeCapabilityRevision(ctx context.Context, service *capability.Service, revisionID string, request webScope) (capability.Detail, int, error) {
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	if authoritative, ok := service.ScopeForRevision(revisionID); ok {
		if authoritative.Organization != organizationID {
			return capability.Detail{}, http.StatusNotFound, fmt.Errorf("capability not found")
		}
		if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{OrganizationID: authoritative.Organization, Namespace: authoritative.Namespace, Repository: authoritative.Repository}); err != nil {
			return capability.Detail{}, http.StatusForbidden, err
		}
		requestedScope, err := capability.NewScope(organizationID, request.Namespace, request.Repository)
		if err != nil {
			return capability.Detail{}, http.StatusBadRequest, err
		}
		if request.Namespace == "" && request.Repository == "" {
			requestedScope = authoritative
		}
		descriptor, err := service.Describe(revisionID, requestedScope)
		if err != nil {
			return capability.Detail{}, http.StatusNotFound, err
		}
		if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{OrganizationID: descriptor.Scope.Organization, Namespace: descriptor.Scope.Namespace, Repository: descriptor.Scope.Repository, ID: descriptor.Identity.ID}); err != nil {
			return capability.Detail{}, http.StatusForbidden, err
		}
		detail, err := service.DescribeDetail(revisionID, requestedScope)
		if err != nil {
			return capability.Detail{}, http.StatusNotFound, err
		}
		if detail.Descriptor.Identity.Kind == capability.KindSkill {
			detail.MaterializeWith = "materialize_skill"
		}
		return detail, http.StatusOK, nil
	}

	// Yanked/retained skill revisions are intentionally absent from new-selection
	// routing. Resolve them from the authoritative catalogue only for an explicit
	// immutable detail URL, authorise that stable resource, and never substitute a
	// replacement revision.
	if s.catalogue == nil {
		return capability.Detail{}, http.StatusNotFound, fmt.Errorf("capability not found")
	}
	info, err := s.catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return capability.Detail{}, http.StatusNotFound, err
	}
	scope, ok := service.ScopeForRepository(organizationID, info.RepositoryID)
	if !ok {
		scope, err = capability.NewScope(organizationID, "", "")
		if err != nil {
			return capability.Detail{}, http.StatusNotFound, err
		}
	}
	if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{OrganizationID: scope.Organization, Namespace: scope.Namespace, Repository: scope.Repository, ID: info.SkillID}); err != nil {
		return capability.Detail{}, http.StatusForbidden, err
	}
	governanceInfo, err := s.catalogue.RevisionGovernance(ctx, organizationID, revisionID)
	if err != nil {
		return capability.Detail{}, http.StatusNotFound, err
	}
	detail := capability.Detail{Descriptor: capability.Descriptor{
		Identity: capability.Identity{ID: info.SkillID, Kind: capability.KindSkill},
		Name: info.Name, Version: info.Version, Compatibility: info.Compatibility, Scope: scope,
		Source: capability.Source{RepositoryID: info.RepositoryID, URL: info.RepositoryURL, Path: info.Path},
		Provenance: capability.Provenance{RevisionID: info.RevisionID, Commit: info.Commit, Tree: info.Tree, ArchiveSHA256TarGZ: info.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: info.ArchiveSHA256ZIP},
		TrustLevel: governanceInfo.TrustLevel, Status: governanceInfo.Status, Governance: governanceInfo.Metadata,
	}, PackageDigests: capability.PackageDigests{TarGZ: info.ArchiveSHA256TarGZ, ZIP: info.ArchiveSHA256ZIP}, MaterializeWith: "materialize_skill"}
	return detail, http.StatusOK, nil
}

func (s *Server) webKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	page := s.baseWebPage(r, "knowledge", "Knowledge")
	page.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	service := webKnowledgeServiceFor(s)
	if service == nil {
		page.Notice = "Knowledge is not configured."
		s.renderWebPage(w, r, http.StatusOK, page)
		return
	}
	if page.Query == "" {
		page.Notice = "Search authorised organisational knowledge."
		s.renderWebPage(w, r, http.StatusOK, page)
		return
	}
	if err := s.authorize(r.Context(), authz.ActionKnowledgeSearch, authz.Resource{}); err != nil {
		s.renderWebError(w, r, page, err)
		return
	}
	response, err := service.SearchOKF(r.Context(), page.Query, 10)
	if err != nil {
		s.renderWebErrorStatus(w, r, page, err, http.StatusInternalServerError)
		return
	}
	for _, result := range response.Results {
		if authorizationPolicyFor(s) != nil {
			if err := s.authorize(r.Context(), authz.ActionKnowledgeSearch, authz.Resource{ID: result.Result.DocumentID}); err != nil {
				continue
			}
		}
		page.KnowledgeResults = append(page.KnowledgeResults, webKnowledgeItem{Result: result, ReadURL: "/ui/knowledge/" + url.PathEscape(result.Result.ChunkID)})
		if len(page.KnowledgeResults) == 5 {
			break
		}
	}
	if len(page.KnowledgeResults) == 0 {
		page.Notice = "No authorised knowledge matched this query."
	}
	s.renderWebPage(w, r, http.StatusOK, page)
}

func (s *Server) webKnowledgeRead(w http.ResponseWriter, r *http.Request) {
	page := s.baseWebPage(r, "knowledge", "Knowledge document")
	service := webKnowledgeServiceFor(s)
	chunkID := strings.TrimSpace(r.PathValue("chunkID"))
	if service == nil || chunkID == "" {
		s.renderWebNotFound(w, r, page)
		return
	}
	documentID, err := service.DocumentIDForChunk(r.Context(), chunkID)
	if err != nil {
		s.renderWebNotFound(w, r, page)
		return
	}
	if err := s.authorize(r.Context(), authz.ActionKnowledgeRead, authz.Resource{ID: documentID}); err != nil {
		s.renderWebError(w, r, page, err)
		return
	}
	read, err := service.ReadOKF(r.Context(), chunkID)
	if err != nil {
		s.renderWebNotFound(w, r, page)
		return
	}
	if authorizationPolicyFor(s) != nil {
		links := read.Links[:0]
		for _, link := range read.Links {
			if link.TargetDocumentID != "" {
				if err := s.authorize(r.Context(), authz.ActionKnowledgeRead, authz.Resource{ID: link.TargetDocumentID}); err != nil {
					continue
				}
			}
			links = append(links, link)
		}
		read.Links = links
	}
	backlinks, err := service.GetBacklinks(r.Context(), documentID, 50)
	if err != nil {
		s.renderWebErrorStatus(w, r, page, err, http.StatusInternalServerError)
		return
	}
	filtered := make([]knowledge.Backlink, 0, 25)
	for _, backlink := range backlinks {
		if authorizationPolicyFor(s) != nil {
			if err := s.authorize(r.Context(), authz.ActionKnowledgeRead, authz.Resource{ID: backlink.DocumentID}); err != nil {
				continue
			}
		}
		filtered = append(filtered, backlink)
		if len(filtered) == 25 {
			break
		}
	}
	page.Title = firstNonEmpty(read.Metadata.Title, read.Chunk.Heading, read.Chunk.Path) + " · Skillet"
	page.Knowledge = &webKnowledgeView{Read: read, Backlinks: filtered, SourceURL: safeHTTPURL(read.Chunk.SourceLocator), SourceText: read.Chunk.SourceLocator}
	s.renderWebPage(w, r, http.StatusOK, page)
}

func webKnowledgeServiceFor(s *Server) *knowledge.Service {
	if s == nil {
		return nil
	}
	value, ok := knowledgeServices.Load(s)
	if !ok {
		return nil
	}
	service, _ := value.(*knowledge.Service)
	return service
}

func (s *Server) baseWebPage(r *http.Request, section, title string) webPageData {
	identity := webIdentity{Organization: s.organizationID}
	if authenticated, ok := Identity(r.Context()); ok {
		identity.Subject = authenticated.Subject
		identity.Organization = authenticated.OrganizationID
		identity.Authenticated = true
	} else {
		identity.Subject = "development"
	}
	page := webPageData{Title: title + " · Skillet", Section: section, Identity: identity}
	page.OperatorVisible = identity.Organization != "" && s.webCanAuthorize(r.Context(), authz.ActionOperatorRead, authz.Resource{OrganizationID: identity.Organization})
	return page
}

func scopeFromRequest(r *http.Request) webScope {
	return webScope{Namespace: strings.TrimSpace(r.URL.Query().Get("namespace")), Repository: strings.TrimSpace(r.URL.Query().Get("repository"))}
}

func filtersFromRequest(r *http.Request) webFilters {
	return webFilters{Kind: strings.TrimSpace(r.URL.Query().Get("kind")), State: strings.TrimSpace(r.URL.Query().Get("state")), Owner: strings.TrimSpace(r.URL.Query().Get("owner")), Trust: strings.TrimSpace(r.URL.Query().Get("trust"))}
}

func webCapabilityMatches(descriptor capability.Descriptor, filters webFilters) bool {
	if filters.Kind != "" && string(descriptor.Identity.Kind) != filters.Kind {
		return false
	}
	if filters.State != "" && string(descriptor.Status) != filters.State {
		return false
	}
	if filters.Owner != "" && !strings.EqualFold(strings.TrimSpace(descriptor.Governance.Owner), filters.Owner) {
		return false
	}
	return true
}

func safeHTTPURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "Knowledge"
}

func sortedMetadata(values map[string]string) [][2]string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if governance.IsControlKey(key) {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([][2]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, [2]string{key, values[key]})
	}
	return out
}

func shortValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 18 {
		return value
	}
	return value[:10] + "…" + value[len(value)-6:]
}

func setBrowserSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", browserCSP)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *Server) renderWebPage(w http.ResponseWriter, r *http.Request, status int, page webPageData) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := webPageComponent(page).Render(r.Context(), w); err != nil && s.log != nil {
		s.log.Error("render web page", "error", err)
	}
}

func (s *Server) renderWebError(w http.ResponseWriter, r *http.Request, page webPageData, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, ErrAuthorizationDenied) {
		status = http.StatusForbidden
	}
	s.renderWebErrorStatus(w, r, page, err, status)
}

func (s *Server) renderWebErrorStatus(w http.ResponseWriter, r *http.Request, page webPageData, err error, status int) {
	page.Error = http.StatusText(status)
	if status < 500 && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderWebPage(w, r, status, page)
}

func (s *Server) renderWebNotFound(w http.ResponseWriter, r *http.Request, page webPageData) {
	page.Error = "Not found"
	s.renderWebPage(w, r, http.StatusNotFound, page)
}

func webPageComponent(page webPageData) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		return webTemplate.ExecuteTemplate(w, "page", page)
	})
}

var webTemplate = template.Must(template.New("page").Funcs(template.FuncMap{
	"join":     strings.Join,
	"short":    shortValue,
	"metadata": sortedMetadata,
}).Parse(webTemplateSource))
