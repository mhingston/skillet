package httpserver

import (
	"context"
	"fmt"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"io"
	"net/http"
	"strings"
	"time"
)

func PackageHandler(store *packagestore.Store, signer packageurl.Signer, organizationID string) http.Handler {
	return PackageHandlerWithMetrics(store, signer, organizationID, nil)
}

func PackageHandlerWithMetrics(store *packagestore.Store, signer packageurl.Signer, organizationID string, metrics *Metrics) http.Handler {
	return PackageHandlerWithMetricsAndAudit(store, signer, organizationID, metrics, nil)
}

type PackageAuditFunc func(context.Context, string, string, map[string]any) error

func PackageHandlerWithMetricsAndAudit(store *packagestore.Store, signer packageurl.Signer, organizationID string, metrics *Metrics, audit PackageAuditFunc) http.Handler {
	return PackageHandlerWithMetricsAndAuditTTL(store, signer, organizationID, metrics, audit, 5*time.Minute)
}

func PackageHandlerWithMetricsAndAuditTTL(store *packagestore.Store, signer packageurl.Signer, organizationID string, metrics *Metrics, audit PackageAuditFunc, urlTTL time.Duration) http.Handler {
	if urlTTL <= 0 {
		urlTTL = 5 * time.Minute
	}
	cacheMaxAge := int(urlTTL / time.Second)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		const prefix = "/v1/packages/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		filePath := strings.TrimPrefix(r.URL.Path, prefix)
		format := ""
		digest := ""
		switch {
		case strings.HasSuffix(filePath, ".tar.gz"):
			format = "tar.gz"
			digest = strings.TrimSuffix(filePath, ".tar.gz")
		case strings.HasSuffix(filePath, ".zip"):
			format = "zip"
			digest = strings.TrimSuffix(filePath, ".zip")
		default:
			http.NotFound(w, r)
			return
		}
		authorizedOrganization := organizationID
		if authenticated, ok := OrganizationID(r.Context()); ok {
			authorizedOrganization = authenticated
		}
		payload, e := signer.Verify(r.URL.Query().Get("token"), authorizedOrganization, time.Now())
		if e != nil || payload.Digest != digest || payload.Format != format {
			if metrics != nil {
				metrics.AuthFailures.Add(1)
			}
			if audit != nil {
				if auditErr := audit(r.Context(), authorizedOrganization, "authentication_authorization_failure", map[string]any{"operation": "package_download", "digest": digest, "format": format}); auditErr != nil && metrics != nil {
					metrics.AuditFailures.Add(1)
				}
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		reader, size, e := store.Open(format, digest)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		defer reader.Close()
		contentType := "application/gzip"
		if format == "zip" {
			contentType = "application/zip"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", size))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", digest+"."+format))
		w.Header().Set("Cache-Control", fmt.Sprintf("private, max-age=%d, immutable", cacheMaxAge))
		w.WriteHeader(http.StatusOK)
		if metrics != nil {
			metrics.PackageDownloads.Add(1)
			metrics.PackageBytes.Add(uint64(size))
		}
		if audit != nil {
			if auditErr := audit(r.Context(), authorizedOrganization, "signed_package_downloaded", map[string]any{"digest": digest, "format": format, "size_bytes": size}); auditErr != nil && metrics != nil {
				metrics.AuditFailures.Add(1)
			}
		}
		_, _ = io.Copy(w, reader)
	})
}
