package catalogue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

type MaterializationReference struct {
	RevisionID        string
	SkillID           string
	Commit            string
	Tree              string
	ArchiveSHA256     string
	MaterializationID string
}

type MaterializationProvenance struct {
	RevisionID        string
	SkillID           string
	Commit            string
	Tree              string
	ArchiveSHA256     string
	MaterializationID string
}

func (s *Store) ValidateMaterialization(ctx context.Context, organizationID string, reference MaterializationReference) (MaterializationProvenance, error) {
	if s == nil || s.DB == nil || organizationID == "" {
		return MaterializationProvenance{}, fmt.Errorf("catalogue database and organization are required")
	}
	if reference.RevisionID == "" || reference.SkillID == "" || reference.Commit == "" || reference.Tree == "" || reference.ArchiveSHA256 == "" || reference.MaterializationID == "" {
		return MaterializationProvenance{}, fmt.Errorf("complete materialization and immutable revision identity is required")
	}

	var skillID, commit, tree, tarDigest, zipDigest string
	err := s.DB.QueryRowContext(ctx, `SELECT sr.skill_id, sr.commit_sha, sr.tree_sha, sr.archive_sha256_tar_gz, sr.archive_sha256_zip
		FROM skill_revisions sr
		JOIN skills sk ON sk.id = sr.skill_id
		WHERE sr.id = ? AND sk.organization_id = ?`, reference.RevisionID, organizationID).Scan(&skillID, &commit, &tree, &tarDigest, &zipDigest)
	if err != nil {
		if err == sql.ErrNoRows {
			return MaterializationProvenance{}, fmt.Errorf("materialization revision is unavailable or outside the organization")
		}
		return MaterializationProvenance{}, err
	}
	if reference.SkillID != skillID || reference.Commit != commit || reference.Tree != tree || (reference.ArchiveSHA256 != tarDigest && reference.ArchiveSHA256 != zipDigest) {
		return MaterializationProvenance{}, fmt.Errorf("materialization identity does not match immutable revision")
	}

	var materializationDetails string
	err = s.DB.QueryRowContext(ctx, `SELECT details_json FROM audit_events
		WHERE organization_id=? AND event_type='materialisation_prepared' AND revision_id=? AND request_id=?
		ORDER BY id DESC LIMIT 1`, organizationID, reference.RevisionID, reference.MaterializationID).Scan(&materializationDetails)
	if err != nil {
		if err == sql.ErrNoRows {
			return MaterializationProvenance{}, fmt.Errorf("materialization is unavailable or outside the organization")
		}
		return MaterializationProvenance{}, err
	}
	var materialization map[string]any
	if err := json.Unmarshal([]byte(materializationDetails), &materialization); err != nil {
		return MaterializationProvenance{}, fmt.Errorf("decode materialization provenance: %w", err)
	}
	if materialization["archive_sha256"] != reference.ArchiveSHA256 {
		return MaterializationProvenance{}, fmt.Errorf("materialization provenance does not match package")
	}

	return MaterializationProvenance{
		RevisionID:        reference.RevisionID,
		SkillID:           skillID,
		Commit:            commit,
		Tree:              tree,
		ArchiveSHA256:     reference.ArchiveSHA256,
		MaterializationID: reference.MaterializationID,
	}, nil
}
