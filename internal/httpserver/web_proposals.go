package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/evidence"
	"github.com/mhingston/skillet/internal/proposal"
)

type webProposalCandidate struct {
	Candidate evidence.Candidate
}

type webProposalListPage struct {
	Title      string
	Identity   webIdentity
	Detail     capability.Detail
	Candidates []webProposalCandidate
	Proposals  []proposal.Proposal
	CanPrepare bool
	CSRFToken  string
	Notice     string
	Error      string
}

type webProposalDetailPage struct {
	Title               string
	Identity            webIdentity
	Proposal            proposal.Proposal
	HandoffJSON         string
	VerificationExample string
	CanAttach           bool
	CanReject           bool
	CSRFToken           string
	Notice              string
	Error               string
}

func (s *Server) addProposalRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /ui/catalogue/{revisionID}/proposals", s.webImprovementProposals)
	mux.HandleFunc("POST /ui/catalogue/{revisionID}/proposals", s.webPrepareImprovementProposal)
	mux.HandleFunc("GET /ui/proposals/{proposalID}", s.webImprovementProposal)
	mux.HandleFunc("POST /ui/proposals/{proposalID}/artifact", s.webAttachImprovementProposal)
	mux.HandleFunc("POST /ui/proposals/{proposalID}/reject", s.webRejectImprovementProposal)
}

func (s *Server) webImprovementProposals(w http.ResponseWriter, r *http.Request) {
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	detail, ok := s.authorisedProposalDetail(r, revisionID, authz.ActionProposalRead)
	if !ok {
		s.renderProposalListNotFound(w, r)
		return
	}
	store, err := s.proposalStore(r.Context())
	if err != nil {
		s.renderProposalListError(w, r, webProposalListPage{Title: "Improvement proposals · Skillet", Identity: s.collaborationIdentity(r.Context()), Detail: detail}, http.StatusServiceUnavailable, err)
		return
	}
	organizationID, _ := s.proposalActor(r.Context())
	items, err := store.ListRevision(r.Context(), organizationID, revisionID, 50)
	if err != nil {
		s.renderProposalListError(w, r, webProposalListPage{Title: "Improvement proposals · Skillet", Identity: s.collaborationIdentity(r.Context()), Detail: detail}, http.StatusInternalServerError, err)
		return
	}
	resource := collaborationResource(detail)
	page := webProposalListPage{
		Title:     detail.Descriptor.Name + " improvement proposals · Skillet",
		Identity:  s.collaborationIdentity(r.Context()),
		Detail:    detail,
		Proposals: items,
	}
	page.CanPrepare = s.webCanAuthorize(r.Context(), authz.ActionProposalPrepare, resource) &&
		s.webCanAuthorize(r.Context(), authz.ActionEvidenceReview, resource) &&
		s.webCanAuthorize(r.Context(), authz.ActionCapabilityDescribe, resource)
	if page.CanPrepare {
		result, evidenceErr := evidence.New(s.catalogue).Candidates(r.Context(), organizationID, evidence.Query{RevisionID: revisionID})
		if evidenceErr != nil {
			s.renderProposalListError(w, r, page, http.StatusInternalServerError, evidenceErr)
			return
		}
		for _, candidate := range result.Candidates {
			page.Candidates = append(page.Candidates, webProposalCandidate{Candidate: candidate})
		}
		page.CSRFToken, _ = s.operatorCSRFToken(r.Context())
	}
	switch r.URL.Query().Get("state") {
	case "rejected":
		page.Notice = "Proposal rejected. Canonical source and the active capability were not changed."
	}
	if len(page.Candidates) == 0 && len(page.Proposals) == 0 && page.Notice == "" {
		page.Notice = "No reviewable improvement evidence or proposals are available for this exact revision."
	}
	s.renderProposalListPage(w, r, http.StatusOK, page)
}

func (s *Server) webPrepareImprovementProposal(w http.ResponseWriter, r *http.Request) {
	revisionID := strings.TrimSpace(r.PathValue("revisionID"))
	detail, ok := s.authorisedProposalDetail(r, revisionID, authz.ActionProposalPrepare)
	if !ok {
		s.renderProposalListNotFound(w, r)
		return
	}
	resource := collaborationResource(detail)
	if !s.webCanAuthorize(r.Context(), authz.ActionEvidenceReview, resource) || !s.webCanAuthorize(r.Context(), authz.ActionCapabilityDescribe, resource) {
		s.renderProposalListNotFound(w, r)
		return
	}
	if !s.parseProposalForm(w, r, 64<<10) {
		return
	}
	organizationID, actorID := s.proposalActor(r.Context())
	candidate, err := s.proposalCandidate(r.Context(), organizationID, revisionID, strings.TrimSpace(r.PostForm.Get("candidate_id")))
	if err != nil {
		s.renderProposalListMutationError(w, r, http.StatusBadRequest, err)
		return
	}
	store, err := s.proposalStore(r.Context())
	if err != nil {
		s.renderProposalListMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	item, err := store.Prepare(r.Context(), proposal.PrepareInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		CorrelationID: strings.TrimSpace(r.PostForm.Get("correlation_id")),
		Candidate: candidate,
		IntendedOutcome: r.PostForm.Get("intended_outcome"),
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, proposal.ErrStaleBase) {
			status = http.StatusConflict
		}
		s.renderProposalListMutationError(w, r, status, err)
		return
	}
	if err := s.recordAudit(r.Context(), organizationID, "improvement_proposal_prepared", map[string]any{
		"actor_type": "human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.ID, "candidate_id": item.CandidateID,
		"request_id": item.CorrelationID,
	}); err != nil {
		s.renderProposalListMutationError(w, r, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/ui/proposals/"+url.PathEscape(item.ID)+"?state=prepared", http.StatusSeeOther)
}

func (s *Server) webImprovementProposal(w http.ResponseWriter, r *http.Request) {
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))
	organizationID, _ := s.proposalActor(r.Context())
	resource, err := s.proposalResource(r.Context(), organizationID, proposalID)
	if err != nil || !s.webCanAuthorize(r.Context(), authz.ActionProposalRead, resource) {
		s.renderProposalDetailNotFound(w, r)
		return
	}
	store, err := s.proposalStore(r.Context())
	if err != nil {
		s.renderProposalDetailError(w, r, webProposalDetailPage{Title: "Improvement proposal · Skillet", Identity: s.collaborationIdentity(r.Context())}, http.StatusServiceUnavailable, err)
		return
	}
	item, err := store.Get(r.Context(), organizationID, proposalID)
	if err != nil {
		s.renderProposalDetailNotFound(w, r)
		return
	}
	handoffJSON, err := json.MarshalIndent(item.Handoff, "", "  ")
	if err != nil {
		s.renderProposalDetailError(w, r, webProposalDetailPage{Title: "Improvement proposal · Skillet", Identity: s.collaborationIdentity(r.Context()), Proposal: item}, http.StatusInternalServerError, err)
		return
	}
	example, _ := json.MarshalIndent([]proposal.VerificationResult{
		{Name: "source_ingestion_validation", Command: "<exact validation command>", Passed: false, ExitCode: 1, Summary: "<result summary>"},
		{Name: "relevant_regression_evals", Command: "<exact regression/eval command>", Passed: false, ExitCode: 1, Summary: "<result summary>"},
	}, "", "  ")
	page := webProposalDetailPage{
		Title:               "Improvement proposal " + item.ID + " · Skillet",
		Identity:            s.collaborationIdentity(r.Context()),
		Proposal:            item,
		HandoffJSON:         string(handoffJSON),
		VerificationExample: string(example),
		CanAttach:           item.Status != proposal.StatusRejected && item.Status != proposal.StatusStale && s.webCanAuthorize(r.Context(), authz.ActionProposalAttach, resource),
		CanReject:           item.Status != proposal.StatusRejected && item.Status != proposal.StatusStale && s.webCanAuthorize(r.Context(), authz.ActionProposalReject, resource),
	}
	if page.CanAttach || page.CanReject {
		page.CSRFToken, _ = s.operatorCSRFToken(r.Context())
	}
	switch r.URL.Query().Get("state") {
	case "prepared":
		page.Notice = "Proposal prepared from exact evidence and immutable base revision. Nothing has been published."
	case "ready":
		page.Notice = "All fixed verification obligations passed. The proposal is ready for external human/source review; it is not authoritative."
	case "draft":
		page.Notice = "Artifact and verification results recorded. One or more obligations did not pass, so the proposal remains draft."
	}
	s.renderProposalDetailPage(w, r, http.StatusOK, page)
}

func (s *Server) webAttachImprovementProposal(w http.ResponseWriter, r *http.Request) {
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))
	organizationID, actorID := s.proposalActor(r.Context())
	resource, err := s.proposalResource(r.Context(), organizationID, proposalID)
	if err != nil || !s.webCanAuthorize(r.Context(), authz.ActionProposalAttach, resource) {
		s.renderProposalDetailNotFound(w, r)
		return
	}
	if !s.parseProposalForm(w, r, 512<<10) {
		return
	}
	var results []proposal.VerificationResult
	if err := json.Unmarshal([]byte(r.PostForm.Get("verification_json")), &results); err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusBadRequest, fmt.Errorf("verification_json must be a JSON array of verification results"))
		return
	}
	store, err := s.proposalStore(r.Context())
	if err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	item, err := store.Attach(r.Context(), proposal.AttachInput{
		OrganizationID: organizationID,
		ActorID: actorID,
		ProposalID: proposalID,
		BaseRevisionID: strings.TrimSpace(r.PostForm.Get("base_revision_id")),
		Patch: r.PostForm.Get("patch"),
		ExternalReference: strings.TrimSpace(r.PostForm.Get("external_reference")),
		Results: results,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, proposal.ErrStaleBase) {
			status = http.StatusConflict
		}
		s.renderProposalDetailMutationError(w, r, status, err)
		return
	}
	if err := s.recordAudit(r.Context(), organizationID, "improvement_proposal_artifact_attached", map[string]any{
		"actor_type": "human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.ID, "patch_sha256": item.PatchSHA256,
		"status": item.Status,
	}); err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusInternalServerError, err)
		return
	}
	state := "draft"
	if item.Status == proposal.StatusReadyForReview {
		state = "ready"
	}
	http.Redirect(w, r, "/ui/proposals/"+url.PathEscape(item.ID)+"?state="+state, http.StatusSeeOther)
}

func (s *Server) webRejectImprovementProposal(w http.ResponseWriter, r *http.Request) {
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))
	organizationID, actorID := s.proposalActor(r.Context())
	resource, err := s.proposalResource(r.Context(), organizationID, proposalID)
	if err != nil || !s.webCanAuthorize(r.Context(), authz.ActionProposalReject, resource) {
		s.renderProposalDetailNotFound(w, r)
		return
	}
	if !s.parseProposalForm(w, r, 4096) {
		return
	}
	store, err := s.proposalStore(r.Context())
	if err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusServiceUnavailable, err)
		return
	}
	item, err := store.Reject(r.Context(), organizationID, proposalID, actorID)
	if err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusBadRequest, err)
		return
	}
	if err := s.recordAudit(r.Context(), organizationID, "improvement_proposal_rejected", map[string]any{
		"actor_type": "human", "actor_id": actorID, "skill_id": item.CapabilityID,
		"revision_id": item.Base.RevisionID, "proposal_id": item.ID,
	}); err != nil {
		s.renderProposalDetailMutationError(w, r, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/ui/catalogue/"+url.PathEscape(item.Base.RevisionID)+"/proposals?state=rejected", http.StatusSeeOther)
}

func (s *Server) authorisedProposalDetail(r *http.Request, revisionID string, action authz.Action) (capability.Detail, bool) {
	service := capabilityServiceFor(s)
	if service == nil || strings.TrimSpace(revisionID) == "" {
		return capability.Detail{}, false
	}
	detail, status, err := s.webDescribeCapabilityRevision(r.Context(), service, revisionID, webScope{})
	if err != nil || status != http.StatusOK {
		return capability.Detail{}, false
	}
	if !s.webCanAuthorize(r.Context(), action, collaborationResource(detail)) {
		return capability.Detail{}, false
	}
	return detail, true
}

func (s *Server) parseProposalForm(w http.ResponseWriter, r *http.Request, maxBytes int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return false
	}
	if !s.validOperatorCSRF(r.Context(), r.PostForm.Get("_csrf")) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

func (s *Server) renderProposalListPage(w http.ResponseWriter, r *http.Request, status int, page webProposalListPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := proposalListTemplate.ExecuteTemplate(w, "proposal-list-page", page); err != nil && s.log != nil {
		s.log.Error("render proposal list page", "error", err)
	}
}

func (s *Server) renderProposalDetailPage(w http.ResponseWriter, r *http.Request, status int, page webProposalDetailPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := proposalDetailTemplate.ExecuteTemplate(w, "proposal-detail-page", page); err != nil && s.log != nil {
		s.log.Error("render proposal detail page", "error", err)
	}
}

func (s *Server) renderProposalListError(w http.ResponseWriter, r *http.Request, page webProposalListPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderProposalListPage(w, r, status, page)
}

func (s *Server) renderProposalDetailError(w http.ResponseWriter, r *http.Request, page webProposalDetailPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderProposalDetailPage(w, r, status, page)
}

func (s *Server) renderProposalListNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderProposalListPage(w, r, http.StatusNotFound, webProposalListPage{Title: "Improvement proposals · Skillet", Identity: s.collaborationIdentity(r.Context()), Error: "Not found"})
}

func (s *Server) renderProposalDetailNotFound(w http.ResponseWriter, r *http.Request) {
	s.renderProposalDetailPage(w, r, http.StatusNotFound, webProposalDetailPage{Title: "Improvement proposal · Skillet", Identity: s.collaborationIdentity(r.Context()), Error: "Not found"})
}

func (s *Server) renderProposalListMutationError(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.renderProposalListError(w, r, webProposalListPage{Title: "Improvement proposals · Skillet", Identity: s.collaborationIdentity(r.Context())}, status, err)
}

func (s *Server) renderProposalDetailMutationError(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.renderProposalDetailError(w, r, webProposalDetailPage{Title: "Improvement proposal · Skillet", Identity: s.collaborationIdentity(r.Context())}, status, err)
}

var proposalListTemplate = template.Must(template.New("proposal-list-page").Funcs(template.FuncMap{"short": shortValue}).Parse(proposalListTemplateSource))
var proposalDetailTemplate = template.Must(template.New("proposal-detail-page").Funcs(template.FuncMap{"short": shortValue}).Parse(proposalDetailTemplateSource))
