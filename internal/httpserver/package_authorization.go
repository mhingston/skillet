package httpserver

import (
	"net/http"
	"strings"
	"time"
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
			s.metrics.AuthFailures.Add(1)
			_ = s.recordAudit(r.Context(), auth.OrganizationID, "authentication_authorization_failure", map[string]any{"operation": "package", "reason": "token"})
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := withAuthenticatedIdentity(r.Context(), identity)
		digest, format := packageIdentityFromPath(r.URL.Path)
		if digest == "" || s.catalogue == nil {
			http.Error(w, "package not found", http.StatusNotFound)
			return
		}
		payload, err := s.packageSigner.Verify(r.URL.Query().Get("token"), identity.OrganizationID, time.Now())
		if err != nil || payload.RevisionID == "" || payload.Digest != digest || payload.Format != format {
			s.metrics.AuthFailures.Add(1)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		info, err := s.catalogue.Revision(ctx, identity.OrganizationID, payload.RevisionID)
		if err != nil || !revisionContainsPackage(info.ArchiveSHA256TarGZ, info.ArchiveSHA256ZIP, format, digest) {
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

func packageIdentityFromPath(path string) (string, string) {
	value := strings.TrimPrefix(path, "/v1/packages/")
	if strings.HasSuffix(value, ".tar.gz") {
		return strings.TrimSuffix(value, ".tar.gz"), "tar.gz"
	}
	if strings.HasSuffix(value, ".zip") {
		return strings.TrimSuffix(value, ".zip"), "zip"
	}
	return "", ""
}

func revisionContainsPackage(tarDigest, zipDigest, format, digest string) bool {
	switch format {
	case "tar.gz":
		return digest != "" && digest == tarDigest
	case "zip":
		return digest != "" && digest == zipDigest
	default:
		return false
	}
}
