package httpserver

import (
	"net/http"
	"strings"
)

func (s *Server) authorizedPackageHandler(next http.Handler) http.Handler {
	if s == nil || next == nil || authorizationPolicyFor(s) == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := Identity(r.Context())
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		digest := packageDigestFromPath(r.URL.Path)
		if digest == "" || s.catalogue == nil {
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}
		info, err := s.catalogue.RevisionByArchiveDigest(r.Context(), identity.OrganizationID, digest)
		if err != nil {
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}
		if err := s.authorizeCapabilityMaterialization(r.Context(), identity.OrganizationID, info); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
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
