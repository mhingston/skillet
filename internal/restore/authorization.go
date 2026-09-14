package restore

import (
	"context"
	"fmt"
	"strings"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/lockfile"
)

// ResolveRevision validates a locked identity against retained authoritative
// catalogue provenance without reading package bytes or issuing a signed URL.
// Authorization callers use this as the pre-package boundary for lock restore.
func (r *Restorer) ResolveRevision(ctx context.Context, skillID string, entry lockfile.Entry) (catalogue.RevisionInfo, error) {
	if r == nil || r.Catalogue == nil {
		return catalogue.RevisionInfo{}, fmt.Errorf("catalogue is required")
	}
	if r.OrganizationID == "" || skillID == "" {
		return catalogue.RevisionInfo{}, fmt.Errorf("organization and skill ID are required")
	}
	if entry.Source.Type != "git" && entry.Source.Type != "local" {
		return catalogue.RevisionInfo{}, fmt.Errorf("unsupported source type %q", entry.Source.Type)
	}
	if entry.Source.RepositoryID == "" || entry.Source.Path == "" || strings.Contains(entry.Source.Path, "\\") || strings.HasPrefix(entry.Source.Path, "/") || strings.Contains(entry.Source.Path, "..") {
		return catalogue.RevisionInfo{}, fmt.Errorf("valid repository ID and safe path are required")
	}
	if entry.Resolved.Commit == "" || entry.Resolved.Tree == "" {
		return catalogue.RevisionInfo{}, fmt.Errorf("exact commit and tree are required")
	}
	if entry.Integrity.Algorithm != "sha256" || (entry.Integrity.Format != "tar.gz" && entry.Integrity.Format != "zip") || !digestPattern.MatchString(entry.Integrity.Archive) {
		return catalogue.RevisionInfo{}, fmt.Errorf("valid sha256 package integrity is required")
	}

	rawRepositoryID, ok := rawRepositoryID(r.OrganizationID, entry.Source.RepositoryID, skillID, entry.Source.Path)
	if !ok {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked skill identity does not match repository and path")
	}
	revisionID := catalogue.RevisionID(r.OrganizationID, rawRepositoryID, entry.Source.Path, entry.Resolved.Commit, entry.Resolved.Tree)
	info, err := r.Catalogue.Revision(ctx, r.OrganizationID, revisionID)
	if err != nil {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked revision is unavailable: %w", err)
	}
	storedRepositoryID := strings.TrimPrefix(info.RepositoryID, r.OrganizationID+"/")
	if info.SkillID != skillID || (entry.Source.RepositoryID != info.RepositoryID && entry.Source.RepositoryID != storedRepositoryID) || info.Path != entry.Source.Path || info.Commit != entry.Resolved.Commit || info.Tree != entry.Resolved.Tree {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked revision identity mismatch")
	}
	if entry.Name != "" && info.Name != entry.Name {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked skill name mismatch")
	}
	if entry.Version != "" && info.Version != entry.Version {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked skill version mismatch")
	}
	digest := info.ArchiveSHA256TarGZ
	if entry.Integrity.Format == "zip" {
		digest = info.ArchiveSHA256ZIP
	}
	if digest != entry.Integrity.Archive {
		return catalogue.RevisionInfo{}, fmt.Errorf("locked package digest mismatch")
	}
	return info, nil
}
