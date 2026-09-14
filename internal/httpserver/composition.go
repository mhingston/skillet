package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/composition"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	collectionsFileEnv          = "SKILLET_COLLECTIONS_FILE"
	maxCompositionIdentities    = 128
	maxCompositionRevisions     = 512
)

var configuredCompositionCollections sync.Map // map[*Server][]composition.Collection

// ConfigureCollections installs an explicit immutable-in-memory projection of
// source-controlled collection manifests. Passing nil/empty removes it. The
// production binary can alternatively point SKILLET_COLLECTIONS_FILE at a
// versioned manifest; no collection state is authored by the browser.
func (s *Server) ConfigureCollections(collections []composition.Collection) error {
	if s == nil {
		return fmt.Errorf("server is unavailable")
	}
	if len(collections) == 0 {
		configuredCompositionCollections.Delete(s)
		return nil
	}
	if len(collections) > composition.MaxCollections {
		return fmt.Errorf("collections exceed limit")
	}
	copyCollections := append([]composition.Collection(nil), collections...)
	seen := map[string]bool{}
	for i := range copyCollections {
		if err := composition.ValidateCollection(copyCollections[i]); err != nil {
			return err
		}
		if seen[copyCollections[i].ID] {
			return fmt.Errorf("duplicate collection %q", copyCollections[i].ID)
		}
		seen[copyCollections[i].ID] = true
		copyCollections[i].Members = append([]composition.Reference(nil), copyCollections[i].Members...)
	}
	sort.Slice(copyCollections, func(i, j int) bool { return copyCollections[i].ID < copyCollections[j].ID })
	configuredCompositionCollections.Store(s, copyCollections)
	return nil
}

func compositionCollectionsFor(s *Server) ([]composition.Collection, error) {
	if s == nil {
		return nil, fmt.Errorf("server is unavailable")
	}
	if value, ok := configuredCompositionCollections.Load(s); ok {
		collections, _ := value.([]composition.Collection)
		return append([]composition.Collection(nil), collections...), nil
	}
	path := strings.TrimSpace(os.Getenv(collectionsFileEnv))
	if path == "" {
		return nil, nil
	}
	manifest, err := composition.LoadManifest(path)
	if err != nil {
		return nil, fmt.Errorf("load composition collections: %w", err)
	}
	return manifest.Collections, nil
}

type resolveCapabilityPlanInput struct {
	CandidateID string               `json:"candidate_id,omitempty"`
	RevisionID  string               `json:"revision_id,omitempty"`
	Collection  string               `json:"collection,omitempty"`
	Scope       capabilityScopeInput `json:"scope,omitempty"`
}

type resolveCapabilityPlanOutput struct {
	Plan composition.Plan `json:"plan"`
}

func addCompositionTools(server *mcp.Server, app *Server) {
	if server == nil || app == nil || capabilityServiceFor(app) == nil {
		return
	}
	mcp.AddTool(server, &mcp.Tool{
		Name: "resolve_capability_plan",
		Description: "Preview the deterministic declared dependency closure for one explicitly selected capability candidate or curated collection. The result locks exact immutable revisions/digests and explains declared edges. It never executes or activates capabilities and never invents dependencies.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, input resolveCapabilityPlanInput) (*mcp.CallToolResult, resolveCapabilityPlanOutput, error) {
		_ = req
		plan, err := app.resolveCapabilityPlan(ctx, input)
		if err != nil {
			return nil, resolveCapabilityPlanOutput{}, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Resolved %d immutable capability revision(s). This is a preview only; no capability was executed or activated.", len(plan.Capabilities))}}}, resolveCapabilityPlanOutput{Plan: plan}, nil
	})
}

func (s *Server) resolveCapabilityPlan(ctx context.Context, input resolveCapabilityPlanInput) (composition.Plan, error) {
	service := capabilityServiceFor(s)
	if service == nil {
		return composition.Plan{}, fmt.Errorf("capability service is unavailable")
	}
	selected := 0
	if strings.TrimSpace(input.CandidateID) != "" { selected++ }
	if strings.TrimSpace(input.RevisionID) != "" { selected++ }
	if strings.TrimSpace(input.Collection) != "" { selected++ }
	if selected != 1 {
		return composition.Plan{}, fmt.Errorf("select exactly one candidate_id, revision_id, or collection")
	}

	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	requestScope, err := capability.NewScope(organizationID, input.Scope.Namespace, input.Scope.Repository)
	if err != nil {
		return composition.Plan{}, err
	}
	collections, err := compositionCollectionsFor(s)
	if err != nil {
		return composition.Plan{}, err
	}

	selection := composition.Selection{}
	seedRefs := []composition.Reference{}
	switch {
	case input.CandidateID != "":
		payload, verifyErr := s.signer.Verify(input.CandidateID, organizationID, time.Now())
		if verifyErr != nil {
			return composition.Plan{}, verifyErr
		}
		descriptor, describeErr := service.Describe(payload.RevisionID, requestScope)
		if describeErr != nil {
			return composition.Plan{}, describeErr
		}
		if authorizeErr := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{
			OrganizationID: descriptor.Scope.Organization, Namespace: descriptor.Scope.Namespace,
			Repository: descriptor.Scope.Repository, ID: descriptor.Identity.ID,
		}); authorizeErr != nil {
			return composition.Plan{}, authorizeErr
		}
		ref := composition.Reference{ID: descriptor.Identity.ID, RevisionID: payload.RevisionID, Kind: descriptor.Identity.Kind}
		selection.Capability = &ref
		seedRefs = append(seedRefs, ref)
	case input.RevisionID != "":
		descriptor, describeErr := service.Describe(input.RevisionID, requestScope)
		if describeErr != nil {
			return composition.Plan{}, fmt.Errorf("capability is unavailable")
		}
		if authorizeErr := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{
			OrganizationID: descriptor.Scope.Organization, Namespace: descriptor.Scope.Namespace,
			Repository: descriptor.Scope.Repository, ID: descriptor.Identity.ID,
		}); authorizeErr != nil {
			return composition.Plan{}, authorizeErr
		}
		ref := composition.Reference{ID: descriptor.Identity.ID, RevisionID: input.RevisionID, Kind: descriptor.Identity.Kind}
		selection.Capability = &ref
		seedRefs = append(seedRefs, ref)
	case input.Collection != "":
		collection, ok := findCollection(collections, input.Collection)
		if !ok {
			return composition.Plan{}, fmt.Errorf("collection is unavailable")
		}
		selection.Collection = collection.ID
		seedRefs = append(seedRefs, collection.Members...)
	}

	snapshot, err := s.authorizedCompositionSnapshot(ctx, service, requestScope, seedRefs, collections)
	if err != nil {
		return composition.Plan{}, err
	}
	plan, err := composition.Resolve(snapshot, selection)
	if err != nil {
		return composition.Plan{}, err
	}
	return plan, nil
}

func (s *Server) authorizedCompositionSnapshot(ctx context.Context, service *capability.Service, requestScope capability.Scope, seeds []composition.Reference, collections []composition.Collection) (composition.Snapshot, error) {
	queue := append([]composition.Reference(nil), seeds...)
	queued := map[string]bool{}
	for _, ref := range queue { queued[ref.ID] = true }
	processed := map[string]bool{}
	nodes := []composition.Node{}

	for len(queue) > 0 {
		if len(processed) >= maxCompositionIdentities {
			return composition.Snapshot{}, fmt.Errorf("composition closure exceeds identity limit")
		}
		ref := queue[0]
		queue = queue[1:]
		if processed[ref.ID] { continue }
		processed[ref.ID] = true

		authoritativeScope, ok := service.ScopeForIdentity(ref.ID)
		if !ok || authoritativeScope.Organization != requestScope.Organization || !authoritativeScope.Allows(requestScope) {
			continue
		}
		if err := s.authorize(ctx, authz.ActionCapabilityDescribe, authz.Resource{
			OrganizationID: authoritativeScope.Organization, Namespace: authoritativeScope.Namespace,
			Repository: authoritativeScope.Repository, ID: ref.ID,
		}); err != nil {
			// Fail closed without disclosing whether the dependency exists. The pure
			// resolver later reports only a generic unavailable dependency.
			continue
		}

		identityNodes, err := s.compositionNodesForIdentity(ctx, service, requestScope, ref.ID, authoritativeScope)
		if err != nil {
			return composition.Snapshot{}, err
		}
		if len(nodes)+len(identityNodes) > maxCompositionRevisions {
			return composition.Snapshot{}, fmt.Errorf("composition closure exceeds revision limit")
		}
		for _, node := range identityNodes {
			nodes = append(nodes, node)
			for _, dependency := range node.Relations.Requires {
				if !queued[dependency.ID] {
					queued[dependency.ID] = true
					queue = append(queue, dependency)
				}
			}
		}
		sort.Slice(queue, func(i, j int) bool { return queue[i].ID < queue[j].ID })
	}
	return composition.Snapshot{Nodes: nodes, Collections: collections}, nil
}

func (s *Server) compositionNodesForIdentity(ctx context.Context, service *capability.Service, requestScope capability.Scope, identityID string, authoritativeScope capability.Scope) ([]composition.Node, error) {
	current, err := service.SelectableDetailsByIdentity(identityID, requestScope)
	if err != nil {
		return nil, err
	}
	kind := capability.Kind("")
	if len(current) > 0 {
		kind = current[0].Descriptor.Identity.Kind
	}
	if kind != capability.KindSkill || s.catalogue == nil {
		out := make([]composition.Node, 0, len(current))
		for _, detail := range current {
			relations, parseErr := composition.ParseMetadata(detail.Descriptor.Metadata)
			if parseErr != nil { return nil, parseErr }
			if detail.Descriptor.Identity.Kind == capability.KindSkill { detail.MaterializeWith = "materialize_skill" }
			out = append(out, composition.Node{Detail: detail, Relations: relations, Selectable: true})
		}
		return out, nil
	}

	revisions, err := s.catalogue.CompositionRevisions(ctx, requestScope.Organization, identityID)
	if err != nil {
		return nil, err
	}
	out := make([]composition.Node, 0, len(revisions))
	for _, revision := range revisions {
		if revision.TrustLevel != "" && revision.TrustLevel != "approved" { continue }
		if revision.Status == capability.StatusYanked { continue }
		state, control, parseErr := governance.Parse(revision.Metadata, governance.Defaults{})
		if parseErr != nil { return nil, parseErr }
		control.Visibility = governance.VisibilityOrganization
		if authoritativeScope.Repository != "" {
			control.Visibility = governance.VisibilityRepository
		} else if authoritativeScope.Namespace != "" {
			control.Visibility = governance.VisibilityNamespace
		}
		detail := capability.Detail{
			Descriptor: capability.Descriptor{
				Identity: capability.Identity{ID: revision.SkillID, Kind: capability.KindSkill},
				Name: revision.Name, Description: revision.Description, Version: revision.Version,
				Compatibility: revision.Compatibility, Scope: authoritativeScope,
				Source: capability.Source{RepositoryID: revision.RepositoryID, URL: revision.RepositoryURL, Path: revision.Path},
				Provenance: capability.Provenance{RevisionID: revision.RevisionID, Commit: revision.Commit, Tree: revision.Tree, ArchiveSHA256TarGZ: revision.ArchiveSHA256TarGZ, ArchiveSHA256ZIP: revision.ArchiveSHA256ZIP},
				TrustLevel: revision.TrustLevel, Status: state, Governance: control,
				Metadata: governance.RoutingMetadata(revision.Metadata), HasScripts: revision.HasScripts,
			},
			PackageDigests: capability.PackageDigests{TarGZ: revision.ArchiveSHA256TarGZ, ZIP: revision.ArchiveSHA256ZIP},
			MaterializeWith: "materialize_skill",
		}
		relations, parseErr := composition.ParseMetadata(detail.Descriptor.Metadata)
		if parseErr != nil { return nil, parseErr }
		out = append(out, composition.Node{Detail: detail, Relations: relations, Selectable: true})
	}
	return out, nil
}

func findCollection(collections []composition.Collection, id string) (composition.Collection, bool) {
	id = strings.TrimSpace(id)
	for _, collection := range collections {
		if collection.ID == id { return collection, true }
	}
	return composition.Collection{}, false
}

type compositionWebPage struct {
	Identity    webIdentity
	Collections []composition.Collection
	Selected   string
	Plan       *composition.Plan
	Error      string
}

func (s *Server) addCompositionWebRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil { return }
	mux.HandleFunc("GET /ui/composition", s.webComposition)
	mux.HandleFunc("GET /ui/composition/preview", s.webComposition)
}

func (s *Server) webComposition(w http.ResponseWriter, r *http.Request) {
	base := s.baseWebPage(r, "catalogue", "Composition preview")
	collections, err := compositionCollectionsFor(s)
	page := compositionWebPage{Identity: base.Identity, Collections: collections, Selected: strings.TrimSpace(r.URL.Query().Get("collection"))}
	if err != nil {
		page.Error = "Composition configuration is unavailable."
		s.renderCompositionPage(w, http.StatusServiceUnavailable, page)
		return
	}
	candidateID := strings.TrimSpace(r.URL.Query().Get("candidate_id"))
	revisionID := strings.TrimSpace(r.URL.Query().Get("revision_id"))
	if page.Selected == "" && candidateID == "" && revisionID == "" {
		s.renderCompositionPage(w, http.StatusOK, page)
		return
	}
	plan, err := s.resolveCapabilityPlan(r.Context(), resolveCapabilityPlanInput{
		CandidateID: candidateID, RevisionID: revisionID, Collection: page.Selected,
		Scope: capabilityScopeInput{Namespace: r.URL.Query().Get("namespace"), Repository: r.URL.Query().Get("repository")},
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrAuthorizationDenied) { status = http.StatusForbidden }
		page.Error = "The composition plan is invalid or unavailable in this authorised scope."
		s.renderCompositionPage(w, status, page)
		return
	}
	page.Plan = &plan
	s.renderCompositionPage(w, http.StatusOK, page)
}

func (s *Server) renderCompositionPage(w http.ResponseWriter, status int, page compositionWebPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := compositionPageTemplate.Execute(w, page); err != nil && s.log != nil {
		s.log.Error("render composition page", "error", err)
	}
}

var compositionPageTemplate = template.Must(template.New("composition").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Composition preview · Skillet</title><link rel="stylesheet" href="/ui/assets/skillet.css"></head>
<body><main class="main" id="main-content"><nav class="breadcrumbs"><a href="/ui/catalogue">Capabilities</a><span>/</span><span aria-current="page">Composition preview</span></nav>
<header class="page-header"><div><span class="eyebrow capability-colour">Deterministic composition</span><h1>Preview declared capability closure</h1><p>Plans contain only source-authored dependencies and curated collection members. Previewing never executes, activates, or materialises a capability.</p></div></header>
{{if .Error}}<section class="state-card error-state" role="alert"><h2>Plan unavailable</h2><p>{{.Error}}</p></section>{{end}}
{{if .Collections}}<section class="detail-section"><h2>Curated collections</h2><form method="get" action="/ui/composition/preview"><label><span>Collection</span><select name="collection">{{range .Collections}}<option value="{{.ID}}" {{if eq $.Selected .ID}}selected{{end}}>{{.Name}}</option>{{end}}</select></label><button class="primary-button" type="submit">Preview</button></form><p class="muted">Collections are declarative membership only: no sequencing, conditions, or automatic invocation.</p></section>{{end}}
{{with .Plan}}<section class="detail-section"><h2>Locked resolution plan</h2><ol>{{range .Capabilities}}<li><strong>{{.Name}}</strong> <code>{{.ID}}</code> {{if .Version}}v{{.Version}}{{end}} — revision <code>{{.RevisionID}}</code>{{if .ArchiveSHA256TarGZ}} · tar.gz <code>{{.ArchiveSHA256TarGZ}}</code>{{end}}</li>{{end}}</ol></section>
<section class="detail-section"><h2>Declared reasons and edges</h2><ul>{{range .Edges}}<li><code>{{.From}}</code> → <code>{{.To}}</code> · {{.Relation}}{{if .Constraint}} ({{.Constraint}}){{end}} — {{.Reason}}</li>{{end}}</ul></section>
{{if .Recommendations}}<section class="detail-section"><h2>Recommended, not selected automatically</h2><ul>{{range .Recommendations}}<li><code>{{.From}}</code> recommends <code>{{.Reference.ID}}</code>{{if .Reference.Version}} ({{.Reference.Version}}){{end}}</li>{{end}}</ul></section>{{end}}
{{if .Conflicts}}<section class="detail-section"><h2>Conflict declarations</h2><ul>{{range .Conflicts}}<li><code>{{.From}}</code> conflicts with <code>{{.Reference.ID}}</code>{{if .Reference.Version}} ({{.Reference.Version}}){{end}}</li>{{end}}</ul></section>{{end}}
<section class="governance-callout"><strong>Preview only</strong><span>No capability was executed or activated. Materialisation remains an explicit separate action.</span></section>{{end}}
</main></body></html>`))
