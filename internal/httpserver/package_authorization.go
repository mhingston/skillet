package httpserver

import (
	"net/http"
	"strings"
)

func (s *Server) authorizedPackageHandler(next http.Handler, auth AuthConfig) http.Handler {
	if s == nil || next == nil || authorizationPolicyFor(s) == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth.Validator == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		identity, err := auth.Validator.Authenticate(r.Header.Get("Authorization"))
		if err != nil {
			if auth.Metrics != nil {
				auth.Metrics.AuthFailures.Add(1)
			}
			if auth.Audit != nil {
				_ = auth.Audit(r.Context(), auth.OrganizationID, "authentication_authorization_failure", map[string]any{"operation": "package", "reason": "token"})
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withAuthenticatedIdentity(r.Context(), identity)
		digest := packageDigestFromPath(r.URL.Path)
		if digest == "" || s.catalogue == nil {
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}
		info, err := s.catalogue.RevisionByArchiveDigest(ctx, identity.OrganizationID, digest)
		if err != nil {
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}
		if err := s.authorizeCapabilityMaterialization(ctx, identity.OrganizationID, info); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func packageDigestFromPath(path string) string {
	value := strings.TrimPrefix(path, "/v1/packages/")
	if strings.HasSuffix(value, ".tar.gz") {
		return strings.TrimSuffix(value, ".tar.gz")
	}
	if strings.HasSuffix(value, ".zip") {
		return strings.TrimSuffix(value, ".zip")
	}
	return ""
}
