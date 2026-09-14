package httpserver

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/collaboration"
)

var collaborationStores sync.Map // map[*Server]*collaboration.Store

type webUsefulnessSignals struct {
	Available                bool
	Materialisations         int
	LifecycleCompleted       int
	LifecycleFailed          int
	EffectivePatterns        int
	WorkaroundCorrections    int
	ImprovementCandidates    int
	EvidenceReviewAuthorised bool
}

type webCollaborationPage struct {
	Title       string
	Identity    webIdentity
	Detail      capability.Detail
	Comments    []collaboration.Comment
	Signals     webUsefulnessSignals
	Watching    bool
	CanComment  bool
	CanWatch    bool
	CanModerate bool
	CSRFToken   string
	Notice      string
	Error       string
}

type webActivityItem struct {
	Activity collaboration.Activity
	Name     string
	URL      string
}

type webActivityPage struct {
	Title    string
	Identity webIdentity
	Items    []webActivityItem
	Notice   string
	Error    string
}

func (s *Server) addCollaborationRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /ui/catalogue/{revisionID}/collaboration", s.webCapabilityCollaboration)
	mux.HandleFunc("POST /ui/catalogue/{revisionID}/collaboration/comments", s.webPostCapabilityComment)
	mux.HandleFunc("POST /ui/catalogue/{revisionID}/collaboration/watch", s.webCapabilityWatch)
	mux.HandleFunc("POST /ui/catalogue/{revisionID}/collaboration/comments/{commentID}/moderate", s.webModerateCapabilityComment)
	mux.HandleFunc("GET /ui/activity", s.webCollaborationActivity)
}

func (s *Server) webCapabilityCollaboration(w http.ResponseWriter, r *http.Request) {
	detail, ok := s.authorisedCollaborationDetail(r.Context(), strings.TrimSpace(r.PathValue("revisionID")), authz.ActionCollaborationRead)
	if !ok {
		s.renderCollaborationNotFound(w, r)
		return
	}
	store, err := s.collaborationStore(r.Context())
	if err != nil {
		s.renderCollaborationError(w, r, webCollaborationPage{Title: "Collaboration · Skillet", Identity: s.collaborationIdentity(r.Context())}, http.StatusServiceUnavailable, err)
		return
	}
	organizationID, actorID := s.collaborationActor(r.Context())
	comments, err := store.Thread(r.Context(), organizationID, detail.Descriptor.Identity.ID, 100)
	if err != nil {
		s.renderCollaborationError(w, r, webCollaborationPage{Title: "Collaboration · Skillet", Identity: s.collaborationIdentity(r.Context())}, http.StatusInternalServerError, err)
		return
	}
	watching, err := store.IsWatching(r.Context(), organizationID, detail.Descriptor.Identity.ID, actorID)
	if err != nil {
		s.renderCollaborationError(w, r, webCollaborationPage{Title: "Collaboration · Skillet", Identity: s.collaborationIdentity(r.Context())}, http.StatusInternalServerError, err)
		return
	}
	page := webCollaborationPage{
		Title:      detail.Descriptor.Name + " collaboration · Skillet",
		Identity:   s.collaborationIdentity(r.Context()),
		Detail:     detail,
		Comments:   comments,
		Watching:   watching,
		CanComment: s.webCanAuthorize(r.Context(), authz.ActionCollaborationComment, collaborationResource(detail)),
		CanWatch:   s.webCanAuthorize(r.Context(), authz.ActionCollaborationWatch, collaborationResource(detail)),
	}
	page.CanModerate = s.isCapabilityMaintainer(r.Context(), detail) || s.webCanAuthorize(r.Context(), authz.ActionCollaborationModerate, collaborationResource(detail))
	if page.CanComment || page.CanWatch || page.CanModerate {
		page.CSRFToken, _ = s.operatorCSRFToken(r.Context())
	}
	page.Signals, err = s.webUsefulness(r.Context(), store, detail)
	if err != nil {
		s.renderCollaborationError(w, r, page, http.StatusInternalServerError, err)
		return
	}
	switch r.URL.Query().Get("state") {
	case "commented":
		page.Notice = "Comment added to this stable capability thread."
	case "watched":
		page.Notice = "Capability is now watched. Future discussion activity will appear in Activity."
	case "unwatched":
		page.Notice = "Capability watch removed."
	case "moderated":
		page.Notice = "Comment moderated. The thread retains a provenance tombstone and audit evidence."
	}
	s.renderCollaborationPage(w, r, http.StatusOK, page)
}

func (s *Server) webPostCapabilityComment(w http.ResponseWriter, r *http.Request) {
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	detail, ok := s.authorisedCollaborationDetail(r.Context(), revisionID, authz.ActionCollaborationComment)
	if !ok {
		s.renderCollaborationNotFound(w, r)
		return
	}
	if !s.parseCollaborationForm(w, r) {
		return
	}
	store, err := s.collaborationStore(r.Context())
	if err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	organizationID, actorID := s.collaborationActor(r.Context())
	boundRevision := ""
	if r.PostForm.Get("bind_revision") == "1" {
		boundRevision = revisionID
	}
	comment, err := store.PostComment(r.Context(), organizationID, detail.Descriptor.Identity.ID, actorID, boundRevision, r.PostForm.Get("body"))
	if err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusBadRequest, err)
		return
	}
	_ = s.recordAudit(r.Context(), organizationID, "capability_comment_posted", map[string]any{
		"actor_type": "human", "actor_id": actorID, "skill_id": detail.Descriptor.Identity.ID,
		"revision_id": boundRevision, "comment_id": strconv.FormatInt(comment.ID, 10),
	})
	s.redirectCollaboration(w, r, revisionID, "commented")
}

func (s *Server) webCapabilityWatch(w http.ResponseWriter, r *http.Request) {
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	detail, ok := s.authorisedCollaborationDetail(r.Context(), revisionID, authz.ActionCollaborationWatch)
	if !ok {
		s.renderCollaborationNotFound(w, r)
		return
	}
	if !s.parseCollaborationForm(w, r) {
		return
	}
	watching := r.PostForm.Get("mode") != "unwatch"
	store, err := s.collaborationStore(r.Context())
	if err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	organizationID, actorID := s.collaborationActor(r.Context())
	if _, err := store.SetWatch(r.Context(), organizationID, detail.Descriptor.Identity.ID, actorID, revisionID, watching); err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusInternalServerError, err)
		return
	}
	event := "capability_unwatched"
	state := "unwatched"
	if watching {
		event, state = "capability_watched", "watched"
	}
	_ = s.recordAudit(r.Context(), organizationID, event, map[string]any{
		"actor_type": "human", "actor_id": actorID, "skill_id": detail.Descriptor.Identity.ID, "revision_id": revisionID,
	})
	s.redirectCollaboration(w, r, revisionID, state)
}

func (s *Server) webModerateCapabilityComment(w http.ResponseWriter, r *http.Request) {
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	detail, ok := s.authorisedCollaborationDetail(r.Context(), revisionID, authz.ActionCollaborationRead)
	if !ok {
		s.renderCollaborationNotFound(w, r)
		return
	}
	if !s.isCapabilityMaintainer(r.Context(), detail) && !s.webCanAuthorize(r.Context(), authz.ActionCollaborationModerate, collaborationResource(detail)) {
		s.renderCollaborationNotFound(w, r)
		return
	}
	if !s.parseCollaborationForm(w, r) {
		return
	}
	commentID, err := strconv.ParseInt(strings.TrimSpace(r.PathValue("commentID")), 10, 64)
	if err != nil || commentID < 1 {
		s.renderCollaborationMutationError(w, r, http.StatusBadRequest, errors.New("invalid comment id"))
		return
	}
	store, err := s.collaborationStore(r.Context())
	if err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	organizationID, actorID := s.collaborationActor(r.Context())
	comment, changed, err := store.Moderate(r.Context(), organizationID, detail.Descriptor.Identity.ID, commentID, actorID, r.PostForm.Get("reason"))
	if err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusNotFound, errors.New("comment not found"))
		return
	}
	if changed {
		if err := s.recordAudit(r.Context(), organizationID, "capability_comment_moderated", map[string]any{
			"actor_type": "human", "actor_id": actorID, "skill_id": detail.Descriptor.Identity.ID,
			"revision_id": comment.RevisionID, "comment_id": strconv.FormatInt(comment.ID, 10),
			"body_sha256": comment.BodySHA256,
		}); err != nil {
			s.renderCollaborationMutationError(w, r, http.StatusInternalServerError, err)
			return
		}
	}
	s.redirectCollaboration(w, r, revisionID, "moderated")
}

func (s *Server) webCollaborationActivity(w http.ResponseWriter, r *http.Request) {
	organizationID, actorID := s.collaborationActor(r.Context())
	page := webActivityPage{Title: "Activity · Skillet", Identity: s.collaborationIdentity(r.Context())}
	store, err := s.collaborationStore(r.Context())
	if err != nil {
		s.renderActivityError(w, r, page, http.StatusServiceUnavailable, err)
		return
	}
	activity, err := store.Activity(r.Context(), organizationID, actorID, 50)
	if err != nil {
		s.renderActivityError(w, r, page, http.StatusInternalServerError, err)
		return
	}
	for _, item := range activity {
		if item.WatchRevisionID == "" {
			continue
		}
		detail, ok := s.authorisedCollaborationDetail(r.Context(), item.WatchRevisionID, authz.ActionCollaborationRead)
		if !ok || detail.Descriptor.Identity.ID != item.CapabilityID {
			continue
		}
		page.Items = append(page.Items, webActivityItem{
			Activity: item,
			Name:     detail.Descriptor.Name,
			URL:      "/ui/catalogue/" + url.PathEscape(item.WatchRevisionID) + "/collaboration",
		})
	}
	if len(page.Items) == 0 {
		page.Notice = "No authorised new discussion activity on watched capabilities."
	}
	s.renderActivityPage(w, r, http.StatusOK, page)
}

func (s *Server) webUsefulness(ctx context.Context, store *collaboration.Store, detail capability.Detail) (webUsefulnessSignals, error) {
	organizationID, _ := s.collaborationActor(ctx)
	signals, err := store.EvidenceSignals(ctx, organizationID, detail.Descriptor.Identity.ID, detail.Descriptor.Provenance.RevisionID)
	if err != nil {
		return webUsefulnessSignals{}, err
	}
	out := webUsefulnessSignals{
		Available:             signals.Available,
		Materialisations:      signals.Materialisations,
		LifecycleCompleted:    signals.LifecycleCompleted,
		LifecycleFailed:       signals.LifecycleFailed,
		EffectivePatterns:     signals.EffectivePatterns,
		WorkaroundCorrections: signals.WorkaroundCorrections,
	}
	if signals.Available && s.catalogue != nil && s.webCanAuthorize(ctx, authz.ActionEvidenceReview, collaborationResource(detail)) {
		out.EvidenceReviewAuthorised = true
		_, candidates, err := s.improvementCandidatesTool(ctx, nil, improvementCandidatesInput{
			SkillID: detail.Descriptor.Identity.ID, RevisionID: detail.Descriptor.Provenance.RevisionID, Limit: 1,
		})
		if err == nil {
			out.ImprovementCandidates = candidates.Total
		} else if !errors.Is(err, ErrAuthorizationDenied) {
			return webUsefulnessSignals{}, err
		}
	}
	return out, nil
}

func (s *Server) authorisedCollaborationDetail(ctx context.Context, revisionID string, action authz.Action) (capability.Detail, bool) {
	service := capabilityServiceFor(s)
	if service == nil || strings.TrimSpace(revisionID) == "" {
		return capability.Detail{}, false
	}
	detail, status, err := s.webDescribeCapabilityRevision(ctx, service, revisionID, webScope{})
	if err != nil || status != http.StatusOK {
		return capability.Detail{}, false
	}
	if err := s.authorize(ctx, action, collaborationResource(detail)); err != nil {
		return capability.Detail{}, false
	}
	return detail, true
}

func collaborationResource(detail capability.Detail) authz.Resource {
	d := detail.Descriptor
	return authz.Resource{
		OrganizationID: d.Scope.Organization,
		Namespace:      d.Scope.Namespace,
		Repository:     d.Scope.Repository,
		ID:             d.Identity.ID,
	}
}

func (s *Server) isCapabilityMaintainer(ctx context.Context, detail capability.Detail) bool {
	identity, ok := Identity(ctx)
	if !ok || strings.TrimSpace(identity.Subject) == "" {
		return false
	}
	for _, maintainer := range detail.Descriptor.Governance.Maintainers {
		if identity.Subject == strings.TrimSpace(maintainer) {
			return true
		}
	}
	return false
}

func (s *Server) collaborationStore(ctx context.Context) (*collaboration.Store, error) {
	if existing, ok := collaborationStores.Load(s); ok {
		store, _ := existing.(*collaboration.Store)
		if store != nil {
			return store, nil
		}
	}
	if s == nil || s.catalogue == nil || s.catalogue.DB == nil {
		return nil, errors.New("collaboration persistence is unavailable")
	}
	store, err := collaboration.New(ctx, s.catalogue.DB)
	if err != nil {
		return nil, err
	}
	actual, _ := collaborationStores.LoadOrStore(s, store)
	resolved, _ := actual.(*collaboration.Store)
	if resolved == nil {
		return nil, errors.New("collaboration persistence is unavailable")
	}
	return resolved, nil
}

func (s *Server) collaborationActor(ctx context.Context) (string, string) {
	organizationID, actorID := s.organizationID, "development"
	if identity, ok := Identity(ctx); ok {
		organizationID, actorID = identity.OrganizationID, identity.Subject
	}
	return organizationID, actorID
}

func (s *Server) collaborationIdentity(ctx context.Context) webIdentity {
	organizationID, actorID := s.collaborationActor(ctx)
	_, authenticated := Identity(ctx)
	return webIdentity{Subject: actorID, Organization: organizationID, Authenticated: authenticated}
}

func (s *Server) parseCollaborationForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	if err := r.ParseForm(); err != nil {
		s.renderCollaborationMutationError(w, r, http.StatusBadRequest, errors.New("invalid form"))
		return false
	}
	if !s.validOperatorCSRF(r.Context(), r.PostForm.Get("_csrf")) {
		s.renderCollaborationMutationError(w, r, http.StatusForbidden, errors.New("invalid CSRF token"))
		return false
	}
	return true
}

func (s *Server) redirectCollaboration(w http.ResponseWriter, r *http.Request, revisionID, state string) {
	http.Redirect(w, r, "/ui/catalogue/"+url.PathEscape(revisionID)+"/collaboration?state="+url.QueryEscape(state), http.StatusSeeOther)
}

func (s *Server) renderCollaborationNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderCollaborationError(w, r, webCollaborationPage{Title: "Collaboration · Skillet", Identity: s.collaborationIdentity(r.Context())}, http.StatusNotFound, errors.New("not found"))
}

func (s *Server) renderCollaborationMutationError(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.renderCollaborationError(w, r, webCollaborationPage{Title: "Collaboration · Skillet", Identity: s.collaborationIdentity(r.Context())}, status, err)
}

func (s *Server) renderCollaborationError(w http.ResponseWriter, r *http.Request, page webCollaborationPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && status != http.StatusNotFound && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderCollaborationPage(w, r, status, page)
}

func (s *Server) renderCollaborationPage(w http.ResponseWriter, r *http.Request, status int, page webCollaborationPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := collaborationTemplate.ExecuteTemplate(w, "collaboration-page", page); err != nil && s.log != nil {
		s.log.Error("render collaboration page", "error", err)
	}
}

func (s *Server) renderActivityError(w http.ResponseWriter, r *http.Request, page webActivityPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && status != http.StatusNotFound && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderActivityPage(w, r, status, page)
}

func (s *Server) renderActivityPage(w http.ResponseWriter, _ *http.Request, status int, page webActivityPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := collaborationTemplate.ExecuteTemplate(w, "activity-page", page); err != nil && s.log != nil {
		s.log.Error("render collaboration activity", "error", err)
	}
}

var collaborationTemplate = template.Must(template.New("collaboration-page").Funcs(template.FuncMap{
	"short": shortValue,
}).Parse(collaborationTemplateSource))

const collaborationTemplateSource = `{{define "collaboration-shell-start"}}<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><link rel="stylesheet" href="/ui/assets/skillet.css"></head><body><a class="skip-link" href="#main-content">Skip to content</a><header class="topbar"><a class="brand" href="/ui/catalogue">Skillet</a><div class="topbar-context"><span class="context-label">Organisation</span><strong>{{.Identity.Organization}}</strong></div><div class="session-state"><span class="status-dot" aria-hidden="true"></span><span>{{.Identity.Subject}}</span></div></header><main class="main" id="main-content">{{end}}
{{define "collaboration-shell-end"}}</main></body></html>{{end}}
{{define "collaboration-page"}}{{template "collaboration-shell-start" .}}{{if .Error}}<section class="state-card error-state" role="alert"><h1>{{.Error}}</h1><p>The requested collaboration resource could not be shown in this authorised scope.</p></section>{{else}}{{$d := .Detail.Descriptor}}<nav class="breadcrumbs" aria-label="Breadcrumb"><a href="/ui/catalogue">Capabilities</a><span>/</span><a href="/ui/catalogue/{{$d.Provenance.RevisionID}}">{{$d.Name}}</a><span>/</span><span aria-current="page">Collaboration</span></nav><header class="page-header"><div><span class="eyebrow capability-colour">Capability collaboration</span><h1>{{$d.Name}}</h1><p>Discussion text is untrusted data. Collaboration never changes source, governance, active revision, or semantic ranking.</p></div></header>{{if .Notice}}<section class="state-card"><p>{{.Notice}}</p></section>{{end}}<div class="detail-grid"><section class="detail-section"><h2>Thread identity</h2><dl class="detail-list"><div><dt>Stable capability</dt><dd><code>{{$d.Identity.ID}}</code></dd></div><div><dt>Viewed revision</dt><dd><code>{{$d.Provenance.RevisionID}}</code></dd></div><div><dt>Owner</dt><dd>{{if $d.Governance.Owner}}{{$d.Governance.Owner}}{{else}}—{{end}}</dd></div><div><dt>Maintainers</dt><dd>{{if $d.Governance.Maintainers}}{{range $i,$m := $d.Governance.Maintainers}}{{if $i}}, {{end}}{{$m}}{{end}}{{else}}—{{end}}</dd></div><div><dt>Governance</dt><dd>{{$d.Status}}</dd></div></dl></section><section class="detail-section"><h2>Watch</h2><p>Activity is stored only in Skillet and starts after you watch. There is no public follower graph.</p>{{if .CanWatch}}<form method="post" action="/ui/catalogue/{{$d.Provenance.RevisionID}}/collaboration/watch"><input type="hidden" name="_csrf" value="{{.CSRFToken}}">{{if .Watching}}<input type="hidden" name="mode" value="unwatch"><button class="secondary-button" type="submit">Unwatch</button>{{else}}<input type="hidden" name="mode" value="watch"><button class="primary-button" type="submit">Watch capability</button>{{end}} <a href="/ui/activity">Activity</a></form>{{else}}<p class="muted">Watch changes are not authorised for this identity.</p>{{end}}</section></div><section class="detail-section"><h2>Evidence-first usefulness</h2><p>No universal quality score is computed. Counts below are factual observations for this immutable revision.</p>{{if .Signals.Available}}<dl class="detail-list"><div><dt>Materialisations prepared</dt><dd>{{.Signals.Materialisations}} <span class="muted">from <code>materialisation_prepared</code> audit events; not activation or success</span></dd></div><div><dt>Lifecycle completed / failed</dt><dd>{{.Signals.LifecycleCompleted}} / {{.Signals.LifecycleFailed}} <span class="muted">from revision-bound lifecycle observations</span></dd></div><div><dt>Effective patterns</dt><dd>{{.Signals.EffectivePatterns}} <span class="muted">from structured feedback</span></dd></div><div><dt>Workarounds / corrections</dt><dd>{{.Signals.WorkaroundCorrections}} <span class="muted">from <code>workaround_required</code> + <code>user_correction</code></span></dd></div>{{if .Signals.EvidenceReviewAuthorised}}<div><dt>Open improvement candidates</dt><dd>{{.Signals.ImprovementCandidates}} <span class="muted">derived from existing evidence semantics</span></dd></div>{{end}}</dl>{{else}}<p class="muted">No persisted skill evidence contract exists for this capability kind/revision, so Skillet does not invent zero-valued success signals.</p>{{end}}</section><section class="detail-section"><h2>Discussion</h2><p>One bounded thread is keyed by stable capability identity across revisions. Plain text is rendered inert; raw HTML is never executed. @mentions are intentionally disabled because Skillet has no trusted identity-discovery directory.</p>{{if .Comments}}{{range .Comments}}<article class="result-card"><div class="card-heading"><strong>{{.ActorID}}</strong><span class="muted">{{.CreatedAt}}</span></div>{{if eq .State "moderated"}}<p class="muted">[comment moderated]</p><dl class="compact-meta"><div><dt>Body digest</dt><dd><code>{{short .BodySHA256}}</code></dd></div><div><dt>Moderated by</dt><dd>{{.ModeratedBy}}</dd></div>{{if .ModerationReason}}<div><dt>Reason</dt><dd>{{.ModerationReason}}</dd></div>{{end}}</dl>{{else}}<pre class="comment-body">{{.Body}}</pre>{{end}}<dl class="compact-meta"><div><dt>Revision context</dt><dd>{{if .RevisionID}}<code>{{.RevisionID}}</code>{{else}}unbound{{end}}</dd></div><div><dt>Comment id</dt><dd>{{.ID}}</dd></div></dl>{{if and $.CanModerate (ne .State "moderated")}}<form method="post" action="/ui/catalogue/{{$d.Provenance.RevisionID}}/collaboration/comments/{{.ID}}/moderate"><input type="hidden" name="_csrf" value="{{$.CSRFToken}}"><label><span>Moderation reason</span><input name="reason" maxlength="512"></label><button class="secondary-button" type="submit">Moderate</button></form>{{end}}</article>{{end}}{{else}}<p class="muted">No comments yet.</p>{{end}}{{if .CanComment}}<form class="search-panel" method="post" action="/ui/catalogue/{{$d.Provenance.RevisionID}}/collaboration/comments"><input type="hidden" name="_csrf" value="{{.CSRFToken}}"><label class="search-field"><span>Add comment</span><textarea name="body" maxlength="8192" required></textarea></label><label><input type="checkbox" name="bind_revision" value="1" checked> Bind this comment to the viewed immutable revision</label><div class="form-actions"><button class="primary-button" type="submit">Post comment</button></div></form>{{else}}<p class="muted">Posting comments is not authorised for this identity.</p>{{end}}</section>{{end}}{{template "collaboration-shell-end" .}}{{end}}
{{define "activity-page"}}{{template "collaboration-shell-start" .}}{{if .Error}}<section class="state-card error-state" role="alert"><h1>{{.Error}}</h1></section>{{else}}<nav class="breadcrumbs"><a href="/ui/catalogue">Capabilities</a><span>/</span><span aria-current="page">Activity</span></nav><header class="page-header"><div><span class="eyebrow capability-colour">Watched capability activity</span><h1>Activity</h1><p>Only new discussion events for capabilities this identity is still authorised to read are shown.</p></div></header>{{if .Notice}}<section class="state-card"><p>{{.Notice}}</p></section>{{end}}{{range .Items}}<article class="result-card"><h2><a href="{{.URL}}">{{.Name}}</a></h2><div class="card-heading"><strong>{{.Activity.ActorID}}</strong><span class="muted">{{.Activity.CreatedAt}}</span></div>{{if eq .Activity.State "moderated"}}<p class="muted">[comment moderated]</p>{{else}}<pre class="comment-body">{{.Activity.Body}}</pre>{{end}}<dl class="compact-meta"><div><dt>Revision context</dt><dd>{{if .Activity.RevisionID}}<code>{{.Activity.RevisionID}}</code>{{else}}unbound{{end}}</dd></div><div><dt>Stable identity</dt><dd><code>{{.Activity.CapabilityID}}</code></dd></div></dl></article>{{end}}{{end}}{{template "collaboration-shell-end" .}}{{end}}`
