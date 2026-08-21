// Package restore resolves lockfile entries to the exact immutable packages
// admitted in the catalogue. It never follows an active-revision pointer.
package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/lockfile"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const defaultURLTTL = 5 * time.Minute

// Restorer resolves and verifies locked packages for one organisation.
type Restorer struct {
	OrganizationID string
	Catalogue      *catalogue.Store
	Packages       *packagestore.Store
	PackageSigner  packageurl.Signer
	PublicBaseURL  string
	URLTTL         time.Duration
	Now            func() time.Time
}

// Package is the verified result for one lockfile entry. The archive bytes
// remain in the package store; callers receive a signed URL to the exact CAS
// object instead of package contents.
type Package struct {
	SkillID       string
	Entry         lockfile.Entry
	Revision      catalogue.RevisionInfo
	Format        string
	Digest        string
	SizeBytes     int64
	DownloadURL   string
	DownloadToken string
}

// Restore resolves every entry in f in deterministic skill-ID order. Any
// invalid, missing, or mismatched entry fails the complete operation.
func (r *Restorer) Restore(ctx context.Context, f lockfile.File) ([]Package, error) {
	if r == nil || r.Catalogue == nil || r.Packages == nil {
		return nil, fmt.Errorf("catalogue and package store are required")
	}
	if r.OrganizationID == "" {
		return nil, fmt.Errorf("organization is required")
	}
	if f.LockfileVersion != 1 {
		return nil, fmt.Errorf("unsupported lockfile version %d", f.LockfileVersion)
	}
	if len(f.Skills) == 0 {
		return []Package{}, nil
	}

	keys := make([]string, 0, len(f.Skills))
	for skillID := range f.Skills {
		keys = append(keys, skillID)
	}
	sort.Strings(keys)
	resolved := make([]Package, 0, len(keys))
	for _, skillID := range keys {
		entry := f.Skills[skillID]
		pkg, err := r.resolve(ctx, skillID, entry)
		if err != nil {
			return nil, fmt.Errorf("restore %q: %w", skillID, err)
		}
		resolved = append(resolved, pkg)
	}
	return resolved, nil
}

func (r *Restorer) resolve(ctx context.Context, skillID string, entry lockfile.Entry) (Package, error) {
	if skillID == "" {
		return Package{}, fmt.Errorf("skill ID is required")
	}
	if entry.Source.Type != "git" {
		return Package{}, fmt.Errorf("unsupported source type %q", entry.Source.Type)
	}
	if entry.Source.RepositoryID == "" || entry.Source.Path == "" {
		return Package{}, fmt.Errorf("repository ID and path are required")
	}
	if strings.Contains(entry.Source.Path, "\\") || strings.HasPrefix(entry.Source.Path, "/") || strings.Contains(entry.Source.Path, "..") {
		return Package{}, fmt.Errorf("unsafe locked skill path")
	}
	if entry.Resolved.Commit == "" || entry.Resolved.Tree == "" {
		return Package{}, fmt.Errorf("exact commit and tree are required")
	}
	if entry.Integrity.Algorithm != "sha256" {
		return Package{}, fmt.Errorf("unsupported integrity algorithm %q", entry.Integrity.Algorithm)
	}
	if entry.Integrity.Format != "tar.gz" && entry.Integrity.Format != "zip" {
		return Package{}, fmt.Errorf("unsupported package format %q", entry.Integrity.Format)
	}
	if !digestPattern.MatchString(entry.Integrity.Archive) {
		return Package{}, fmt.Errorf("invalid archive digest")
	}

	rawRepositoryID, ok := rawRepositoryID(r.OrganizationID, entry.Source.RepositoryID, skillID, entry.Source.Path)
	if !ok {
		return Package{}, fmt.Errorf("locked skill identity does not match repository and path")
	}
	revisionID := catalogue.RevisionID(r.OrganizationID, rawRepositoryID, entry.Source.Path, entry.Resolved.Commit, entry.Resolved.Tree)
	info, err := r.Catalogue.Revision(ctx, r.OrganizationID, revisionID)
	if err != nil {
		return Package{}, fmt.Errorf("locked revision is unavailable: %w", err)
	}
	storedRepositoryID := info.RepositoryID
	if strings.HasPrefix(storedRepositoryID, r.OrganizationID+"/") {
		storedRepositoryID = strings.TrimPrefix(storedRepositoryID, r.OrganizationID+"/")
	}
	if info.SkillID != skillID || (entry.Source.RepositoryID != info.RepositoryID && entry.Source.RepositoryID != storedRepositoryID) || info.Path != entry.Source.Path || info.Commit != entry.Resolved.Commit || info.Tree != entry.Resolved.Tree {
		return Package{}, fmt.Errorf("locked revision identity mismatch")
	}
	if entry.Name != "" && info.Name != entry.Name {
		return Package{}, fmt.Errorf("locked skill name mismatch")
	}
	if entry.Version != "" && info.Version != entry.Version {
		return Package{}, fmt.Errorf("locked skill version mismatch")
	}

	digest := info.ArchiveSHA256TarGZ
	if entry.Integrity.Format == "zip" {
		digest = info.ArchiveSHA256ZIP
	}
	if digest != entry.Integrity.Archive {
		return Package{}, fmt.Errorf("locked package digest mismatch")
	}
	data, err := r.Packages.Get(entry.Integrity.Format, entry.Integrity.Archive)
	if err != nil {
		return Package{}, fmt.Errorf("locked package verification failed: %w", err)
	}

	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now().UTC()
	}
	ttl := r.URLTTL
	if ttl <= 0 {
		ttl = defaultURLTTL
	}
	expires := now.Add(ttl)
	token, err := r.PackageSigner.Sign(packageurl.Payload{Version: 1, OrganizationID: r.OrganizationID, Digest: entry.Integrity.Archive, Format: entry.Integrity.Format, ExpiresAt: expires.Unix()})
	if err != nil {
		return Package{}, fmt.Errorf("sign locked package URL: %w", err)
	}
	base := strings.TrimRight(r.PublicBaseURL, "/")
	if base == "" {
		return Package{}, fmt.Errorf("public base URL is required")
	}
	download := base + "/v1/packages/" + entry.Integrity.Archive + "." + entry.Integrity.Format + "?token=" + url.QueryEscape(token)
	return Package{SkillID: skillID, Entry: entry, Revision: info, Format: entry.Integrity.Format, Digest: entry.Integrity.Archive, SizeBytes: int64(len(data)), DownloadURL: download, DownloadToken: token}, nil
}

func rawRepositoryID(organizationID, sourceRepositoryID, skillID, path string) (string, bool) {
	if strings.HasPrefix(sourceRepositoryID, organizationID+"/") {
		raw := strings.TrimPrefix(sourceRepositoryID, organizationID+"/")
		return raw, skillID == sourceRepositoryID+"/"+path
	}
	return sourceRepositoryID, skillID == organizationID+"/"+sourceRepositoryID+"/"+path
}

// VerifyDigest is exposed for callers that need to verify bytes obtained from
// the signed URL before handing them to an extractor.
func VerifyDigest(data []byte, expected string) error {
	if !digestPattern.MatchString(expected) {
		return fmt.Errorf("invalid expected digest")
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expected {
		return fmt.Errorf("package digest mismatch")
	}
	return nil
}
