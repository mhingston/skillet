package httpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"sync"
	"time"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/catalogue"
)

var operatorCSRFSecrets sync.Map // map[*Server][]byte

type webOperatorHealth struct {
	SQLite                 bool
	PackageStore           bool
	LexicalIndex           bool
	InitialSync            bool
	RepositorySyncs        uint64
	RepositorySyncFailures uint64
	LastRepositorySuccess  string
	AuditWriteFailures     uint64
}

type webOperatorPage struct {
	Title                string
	Identity             webIdentity
	Snapshot             catalogue.OperatorSnapshot
	AuditExport          catalogue.AuditExportStatus
	Health               webOperatorHealth
	AuthorizationMode    string
	CapabilityConfigured bool
	KnowledgeConfigured  bool
	CanAuditProbe        bool
	CSRFToken            string
	Notice               string
	Error                string
}

func (s *Server) addOperatorRoutes(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("GET /ui/operator", s.webOperator)
	mux.HandleFunc("POST /ui/operator/audit-probe", s.webOperatorAuditProbe)
	s.addCompositionWebRoutes(mux)
	s.addDistributionRoutes(mux)
	s.addCollaborationRoutes(mux)
	s.addProposalRoutes(mux)
}

func (s *Server) webOperator(w http.ResponseWriter, r *http.Request) {
	base := s.baseWebPage(r, "operator", "Operator")
	page := webOperatorPage{Title: "Operator · Skillet", Identity: base.Identity}
	organizationID := strings.TrimSpace(base.Identity.Organization)
	if organizationID == "" {
		s.renderOperatorError(w, r, page, http.StatusForbidden, errors.New("operator organization is unavailable"))
		return
	}
	if err := s.authorize(r.Context(), authz.ActionOperatorRead, authz.Resource{OrganizationID: organizationID}); err != nil {
		s.renderOperatorError(w, r, page, http.StatusForbidden, err)
		return
	}
	if s.catalogue == nil {
		s.renderOperatorError(w, r, page, http.StatusServiceUnavailable, errors.New("operator catalogue is unavailable"))
		return
	}

	snapshot, err := s.catalogue.OperatorSnapshot(r.Context(), organizationID, 50)
	if err != nil {
		s.renderOperatorError(w, r, page, http.StatusInternalServerError, err)
		return
	}
	page.Snapshot = snapshot
	page.AuditExport = s.catalogue.AuditExportStatus()
	page.CapabilityConfigured = capabilityServiceFor(s) != nil
	page.KnowledgeConfigured = webKnowledgeServiceFor(s) != nil
	page.AuthorizationMode = "compatibility"
	if authorizationPolicyFor(s) != nil {
		page.AuthorizationMode = "claims policy"
	} else if !page.Identity.Authenticated {
		page.AuthorizationMode = "development"
	}
	page.Health = s.operatorHealth()
	page.CanAuditProbe = s.webCanAuthorize(r.Context(), authz.ActionOperatorAuditProbe, authz.Resource{OrganizationID: organizationID})
	if page.CanAuditProbe {
		page.CSRFToken, err = s.operatorCSRFToken(r.Context())
		if err != nil {
			page.CanAuditProbe = false
		}
	}
	if r.URL.Query().Get("probe") == "recorded" {
		page.Notice = "Audit export probe recorded locally and offered to the configured exporter."
	}
	s.renderOperatorPage(w, r, http.StatusOK, page)
}

func (s *Server) webOperatorAuditProbe(w http.ResponseWriter, r *http.Request) {
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(r.Context()); ok {
		organizationID = authenticated
	}
	if err := s.authorize(r.Context(), authz.ActionOperatorAuditProbe, authz.Resource{OrganizationID: organizationID}); err != nil {
		base := s.baseWebPage(r, "operator", "Operator")
		s.renderOperatorError(w, r, webOperatorPage{Title: "Operator · Skillet", Identity: base.Identity}, http.StatusForbidden, err)
		return
	}
	if s.catalogue == nil {
		base := s.baseWebPage(r, "operator", "Operator")
		s.renderOperatorError(w, r, webOperatorPage{Title: "Operator · Skillet", Identity: base.Identity}, http.StatusServiceUnavailable, errors.New("operator catalogue is unavailable"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil || !s.validOperatorCSRF(r.Context(), r.PostForm.Get("_csrf")) {
		base := s.baseWebPage(r, "operator", "Operator")
		s.renderOperatorError(w, r, webOperatorPage{Title: "Operator · Skillet", Identity: base.Identity}, http.StatusForbidden, errors.New("invalid CSRF token"))
		return
	}

	actorID := "development"
	if identity, ok := Identity(r.Context()); ok {
		actorID = identity.Subject
	}
	if err := s.recordAudit(r.Context(), organizationID, "operator_audit_probe", map[string]any{
		"actor_type": "human",
		"actor_id":   actorID,
	}); err != nil {
		base := s.baseWebPage(r, "operator", "Operator")
		s.renderOperatorError(w, r, webOperatorPage{Title: "Operator · Skillet", Identity: base.Identity}, http.StatusInternalServerError, err)
		return
	}
	http.Redirect(w, r, "/ui/operator?probe=recorded", http.StatusSeeOther)
}

func (s *Server) webCanAuthorize(ctx context.Context, action authz.Action, resource authz.Resource) bool {
	policy := authorizationPolicyFor(s)
	if policy == nil {
		return true
	}
	identity, ok := Identity(ctx)
	if !ok {
		return false
	}
	if resource.OrganizationID == "" {
		resource.OrganizationID = identity.OrganizationID
	}
	return policy.Authorize(ctx, identity, action, resource).Allowed
}

func (s *Server) operatorHealth() webOperatorHealth {
	if s == nil {
		return webOperatorHealth{}
	}
	health := webOperatorHealth{
		SQLite:                 s.ready.SQLite.Load(),
		PackageStore:           s.ready.PackageStore.Load(),
		LexicalIndex:           s.ready.LexicalIndex.Load(),
		InitialSync:            s.ready.InitialSync.Load(),
		RepositorySyncs:        s.metrics.RepositorySyncs.Load(),
		RepositorySyncFailures: s.metrics.RepositorySyncFailures.Load(),
		AuditWriteFailures:     s.metrics.AuditFailures.Load(),
	}
	if nanos := s.metrics.RepositoryLastSuccessUnixNanos.Load(); nanos > 0 {
		health.LastRepositorySuccess = time.Unix(0, nanos).UTC().Format(time.RFC3339)
	}
	return health
}

func (s *Server) operatorCSRFToken(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("operator server is unavailable")
	}
	secret, err := operatorCSRFSecret(s)
	if err != nil {
		return "", err
	}
	organizationID, subject := s.organizationID, "development"
	if identity, ok := Identity(ctx); ok {
		organizationID, subject = identity.OrganizationID, identity.Subject
	}
	if strings.TrimSpace(organizationID) == "" || strings.TrimSpace(subject) == "" {
		return "", errors.New("operator CSRF identity is unavailable")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(organizationID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(subject))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *Server) validOperatorCSRF(ctx context.Context, provided string) bool {
	expected, err := s.operatorCSRFToken(ctx)
	if err != nil || provided == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func operatorCSRFSecret(s *Server) ([]byte, error) {
	if existing, ok := operatorCSRFSecrets.Load(s); ok {
		secret, _ := existing.([]byte)
		if len(secret) == 32 {
			return secret, nil
		}
	}
	candidate := make([]byte, 32)
	if _, err := rand.Read(candidate); err != nil {
		return nil, err
	}
	actual, _ := operatorCSRFSecrets.LoadOrStore(s, candidate)
	secret, _ := actual.([]byte)
	if len(secret) != 32 {
		return nil, errors.New("operator CSRF secret is invalid")
	}
	return secret, nil
}

func (s *Server) renderOperatorPage(w http.ResponseWriter, r *http.Request, status int, page webOperatorPage) {
	setBrowserSecurityHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := operatorTemplate.ExecuteTemplate(w, "operator-page", page); err != nil && s.log != nil {
		s.log.Error("render operator page", "error", err)
	}
}

func (s *Server) renderOperatorError(w http.ResponseWriter, r *http.Request, page webOperatorPage, status int, err error) {
	page.Error = http.StatusText(status)
	if status < 500 && !errors.Is(err, ErrAuthorizationDenied) {
		page.Error = err.Error()
	}
	s.renderOperatorPage(w, r, status, page)
}

var operatorTemplate = template.Must(template.New("operator-page").Funcs(template.FuncMap{
	"short": shortValue,
}).Parse(operatorTemplateSource))
