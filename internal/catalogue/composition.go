package catalogue

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	semver "github.com/Masterminds/semver/v3"
	"github.com/mhingston/skillet/internal/governance"
)

// CompositionRevision is the bounded immutable catalogue projection needed by
// deterministic dependency resolution. It contains source-authored metadata
// and provenance only; callers remain responsible for authorization and scope
// checks before disclosure.
type CompositionRevision struct {
	RevisionInfo
	Description string
	Metadata    map[string]string
	TrustLevel  string
	Status      governance.State
	HasScripts  bool
}

// CompositionRevisions returns retained immutable revisions for one already
// authorized stable skill identity. Ordering is deterministic: valid SemVer
// descending, then revision id. Historical rows remain candidates so declared
// exact/range constraints use the same retained-version model as ResolveVersion.
func (s *Store) CompositionRevisions(ctx context.Context, organizationID, skillID string) ([]CompositionRevision, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("catalogue is unavailable")
	}
	if organizationID == "" || skillID == "" {
		return nil, fmt.Errorf("organization and skill ID are required")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.skill_id, r.name, r.description, COALESCE(r.version, ''), r.license, r.compatibility, r.allowed_tools, r.metadata_json, sk.repository_id, repo.url, repo.trust_level, sk.relative_path, r.commit_sha, r.tree_sha, r.archive_sha256_tar_gz, r.archive_sha256_zip, r.has_scripts
		FROM skill_revisions r
		JOIN skills sk ON sk.id=r.skill_id
		JOIN repositories repo ON repo.id=sk.repository_id
		WHERE r.skill_id=? AND sk.organization_id=? AND r.state IN ('active','superseded','removed_from_source')`, skillID, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CompositionRevision{}
	for rows.Next() {
		var item CompositionRevision
		var metadataJSON string
		var hasScripts int
		if err := rows.Scan(
			&item.RevisionID, &item.SkillID, &item.Name, &item.Description, &item.Version,
			&item.License, &item.Compatibility, &item.AllowedTools, &metadataJSON,
			&item.RepositoryID, &item.RepositoryURL, &item.TrustLevel, &item.Path,
			&item.Commit, &item.Tree, &item.ArchiveSHA256TarGZ, &item.ArchiveSHA256ZIP, &hasScripts,
		); err != nil {
			return nil, err
		}
		item.HasScripts = hasScripts == 1
		item.Metadata = map[string]string{}
		if metadataJSON != "" {
			if err := json.Unmarshal([]byte(metadataJSON), &item.Metadata); err != nil {
				return nil, fmt.Errorf("decode composition metadata for revision %q: %w", item.RevisionID, err)
			}
		}
		state, _, err := governance.Parse(item.Metadata, governance.Defaults{})
		if err != nil {
			return nil, fmt.Errorf("revision %q governance: %w", item.RevisionID, err)
		}
		item.Status = state
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		a, aerr := semver.StrictNewVersion(out[i].Version)
		b, berr := semver.StrictNewVersion(out[j].Version)
		switch {
		case aerr == nil && berr == nil && !a.Equal(b):
			return a.GreaterThan(b)
		case aerr == nil && berr != nil:
			return true
		case aerr != nil && berr == nil:
			return false
		default:
			return out[i].RevisionID < out[j].RevisionID
		}
	})
	return out, nil
}
