package catalogue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/search"
)

type Repository struct{ ID, OrganizationID, URL, Ref, TrustLevel, Owner string }
type PackageDigests struct{ TarGZ, ZIP string }
type Revision struct {
	ID, SkillID, CommitSHA, TreeSHA, ArchiveSHA256TarGZ, ArchiveSHA256ZIP, State string
	ValidationResult                                                             any
}
type RevisionInfo struct{ RevisionID, SkillID, Name, License, Compatibility, AllowedTools, RepositoryID, RepositoryURL, Path, Commit, Tree, ArchiveSHA256TarGZ, ArchiveSHA256ZIP string }
type Admission struct {
	Skill     discovery.Skill
	CommitSHA string
	TreeSHA   string
	Packages  PackageDigests
}

type Store struct {
	DB       *sql.DB
	Packages *packagestore.Store
}

func (s *Store) RecordAudit(ctx context.Context, organizationID, eventType string, details any) error {
	if s == nil || s.DB == nil || organizationID == "" || eventType == "" {
		return fmt.Errorf("audit store and event identity are required")
	}
	payload := "{}"
	values := map[string]string{}
	if details != nil {
		encoded, err := json.Marshal(details)
		if err != nil {
			return err
		}
		payload = string(encoded)
		if object, ok := details.(map[string]any); ok {
			for _, key := range []string{"actor_type", "actor_id", "repository_id", "skill_id", "revision_id", "request_id"} {
				if value, ok := object[key].(string); ok {
					values[key] = value
				}
			}
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO audit_events(organization_id, actor_type, actor_id, event_type, repository_id, skill_id, revision_id, request_id, details_json, occurred_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, organizationID, values["actor_type"], values["actor_id"], eventType, values["repository_id"], values["skill_id"], values["revision_id"], values["request_id"], payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func New(db *sql.DB, packages ...*packagestore.Store) *Store {
	s := &Store{DB: db}
	if len(packages) > 0 {
		s.Packages = packages[0]
	}
	return s
}

func RevisionID(organizationID, repositoryID, relativePath, commitSHA, treeSHA string) string {
	sum := sha256.Sum256([]byte(organizationID + "\x00" + repositoryID + "\x00" + relativePath + "\x00" + commitSHA + "\x00" + treeSHA))
	return "rev_" + hex.EncodeToString(sum[:16])
}

func (s *Store) Admit(ctx context.Context, repo Repository, skill discovery.Skill, commitSHA, treeSHA string, packages PackageDigests) (Revision, error) {
	results, err := s.AdmitBatch(ctx, repo, []Admission{{Skill: skill, CommitSHA: commitSHA, TreeSHA: treeSHA, Packages: packages}})
	if err != nil {
		return Revision{}, err
	}
	return results[0], nil
}

// AdmitBatch validates all packages and switches all active pointers in one
// SQLite transaction. A source revision therefore cannot expose a partial
// multi-skill update.
func (s *Store) AdmitBatch(ctx context.Context, repo Repository, admissions []Admission) ([]Revision, error) {
	return s.admitBatch(ctx, repo, admissions, nil)
}

// AdmitBatchAndMarkMissing atomically admits the supplied revisions and removes
// active visibility for skills absent from the same successful source scan.
func (s *Store) AdmitBatchAndMarkMissing(ctx context.Context, repo Repository, admissions []Admission, presentPaths map[string]struct{}) ([]Revision, error) {
	return s.admitBatch(ctx, repo, admissions, presentPaths)
}

func (s *Store) admitBatch(ctx context.Context, repo Repository, admissions []Admission, presentPaths map[string]struct{}) ([]Revision, error) {
	if s == nil || s.DB == nil {
		return nil, fmt.Errorf("catalogue database is required")
	}
	if s.Packages == nil {
		return nil, fmt.Errorf("package store is required for admission")
	}
	if len(admissions) == 0 && presentPaths == nil {
		return []Revision{}, nil
	}
	repoKey := repo.OrganizationID + "/" + repo.ID
	for _, admission := range admissions {
		if admission.Skill.State != discovery.Admitted {
			return nil, fmt.Errorf("cannot admit skill in state %q", admission.Skill.State)
		}
		if admission.CommitSHA == "" || admission.TreeSHA == "" || admission.Packages.TarGZ == "" || admission.Packages.ZIP == "" {
			return nil, fmt.Errorf("commit, tree, and both package digests are required")
		}
		if _, err := s.Packages.Get("tar.gz", admission.Packages.TarGZ); err != nil {
			return nil, fmt.Errorf("tar.gz package integrity: %w", err)
		}
		if _, err := s.Packages.Get("zip", admission.Packages.ZIP); err != nil {
			return nil, fmt.Errorf("zip package integrity: %w", err)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	results := make([]Revision, 0, len(admissions))
	for _, admission := range admissions {
		skill := admission.Skill
		commitSHA, treeSHA, packages := admission.CommitSHA, admission.TreeSHA, admission.Packages
		skillID := repo.OrganizationID + "/" + repo.ID + "/" + skill.RelativePath
		revisionID := RevisionID(repo.OrganizationID, repo.ID, skill.RelativePath, commitSHA, treeSHA)
		validation, err := json.Marshal(skill.Findings)
		if err != nil {
			return nil, err
		}
		metadata, err := json.Marshal(skill.Frontmatter.Metadata)
		if err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO organizations(id) VALUES (?) ON CONFLICT(id) DO NOTHING`, repo.OrganizationID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO repositories(id, organization_id, url, tracked_ref, trust_level, owner) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET url=excluded.url, tracked_ref=excluded.tracked_ref, trust_level=excluded.trust_level, owner=excluded.owner`, repoKey, repo.OrganizationID, repo.URL, repo.Ref, repo.TrustLevel, repo.Owner); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO skills(id, organization_id, repository_id, relative_path, name, owner, searchable) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO UPDATE SET name=excluded.name, owner=excluded.owner, searchable=excluded.searchable`, skillID, repo.OrganizationID, repoKey, skill.RelativePath, skill.Frontmatter.Name, repo.Owner, boolInt(skill.Searchable)); err != nil {
			return nil, err
		}
		var prior string
		_ = tx.QueryRowContext(ctx, `SELECT active_revision_id FROM skills WHERE id=?`, skillID).Scan(&prior)
		if _, err = tx.ExecContext(ctx, `INSERT INTO skill_revisions(id, skill_id, commit_sha, tree_sha, name, description, license, compatibility, allowed_tools, metadata_json, archive_sha256_tar_gz, archive_sha256_zip, has_scripts, state, validation_result_json, admitted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?) ON CONFLICT(id) DO UPDATE SET archive_sha256_tar_gz=excluded.archive_sha256_tar_gz, archive_sha256_zip=excluded.archive_sha256_zip, has_scripts=excluded.has_scripts, license=excluded.license, compatibility=excluded.compatibility, allowed_tools=excluded.allowed_tools, state='active'`, revisionID, skillID, commitSHA, treeSHA, skill.Frontmatter.Name, skill.Frontmatter.Description, skill.Frontmatter.License, skill.Frontmatter.Compatibility, skill.Frontmatter.AllowedTools, string(metadata), packages.TarGZ, packages.ZIP, boolInt(skill.HasScripts), string(validation), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		if prior != "" && prior != revisionID {
			if _, err = tx.ExecContext(ctx, `UPDATE skill_revisions SET state='superseded' WHERE id=?`, prior); err != nil {
				return nil, err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(organization_id, event_type, skill_id, revision_id, details_json, occurred_at) VALUES (?, 'active_revision_changed', ?, ?, ?, ?)`, repo.OrganizationID, skillID, revisionID, `{"previous_revision_id":"`+prior+`"}`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE skills SET active_revision_id=? WHERE id=?`, revisionID, skillID); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(organization_id, event_type, details_json, occurred_at) VALUES (?, 'skill_admitted', ?, ?)`, repo.OrganizationID, string(validation), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return nil, err
		}
		results = append(results, Revision{ID: revisionID, SkillID: skillID, CommitSHA: commitSHA, TreeSHA: treeSHA, ArchiveSHA256TarGZ: packages.TarGZ, ArchiveSHA256ZIP: packages.ZIP, State: "active", ValidationResult: skill.Findings})
	}
	if presentPaths != nil {
		rows, err := tx.QueryContext(ctx, `SELECT id, relative_path, active_revision_id FROM skills WHERE organization_id=? AND repository_id=? AND active_revision_id IS NOT NULL`, repo.OrganizationID, repoKey)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var skillID, path, revisionID string
			if err := rows.Scan(&skillID, &path, &revisionID); err != nil {
				return nil, err
			}
			if _, present := presentPaths[path]; present {
				continue
			}
			if _, err := tx.ExecContext(ctx, `UPDATE skill_revisions SET state='removed_from_source' WHERE id=? AND state='active'`, revisionID); err != nil {
				return nil, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE skills SET active_revision_id=NULL WHERE id=?`, skillID); err != nil {
				return nil, err
			}
			details, _ := json.Marshal(map[string]string{"skill_id": skillID, "reason": "removed_from_source"})
			if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(organization_id, event_type, skill_id, revision_id, details_json, occurred_at) VALUES (?, 'active_revision_changed', ?, ?, ?, ?)`, repo.OrganizationID, skillID, revisionID, string(details), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return nil, err
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *Store) RecordQuarantine(ctx context.Context, repo Repository, skill discovery.Skill, commitSHA, treeSHA string) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("catalogue database is required")
	}
	if commitSHA == "" || treeSHA == "" {
		return fmt.Errorf("commit and tree are required")
	}
	repoKey := repo.OrganizationID + "/" + repo.ID
	skillID := repo.OrganizationID + "/" + repo.ID + "/" + skill.RelativePath
	revisionID := RevisionID(repo.OrganizationID, repo.ID, skill.RelativePath, commitSHA, treeSHA)
	findings, err := json.Marshal(skill.Findings)
	if err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO organizations(id) VALUES (?) ON CONFLICT(id) DO NOTHING`, repo.OrganizationID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO repositories(id, organization_id, url, tracked_ref, trust_level, owner) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, repoKey, repo.OrganizationID, repo.URL, repo.Ref, repo.TrustLevel, repo.Owner); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO skills(id, organization_id, repository_id, relative_path, searchable) VALUES (?, ?, ?, ?, 0) ON CONFLICT(id) DO NOTHING`, skillID, repo.OrganizationID, repoKey, skill.RelativePath); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO skill_revisions(id, skill_id, commit_sha, tree_sha, state, validation_result_json) VALUES (?, ?, ?, ?, 'quarantined', ?) ON CONFLICT(id) DO NOTHING`, revisionID, skillID, commitSHA, treeSHA, string(findings)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO audit_events(organization_id, event_type, details_json) VALUES (?, 'skill_quarantined', ?)`, repo.OrganizationID, string(findings)); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkMissingFromSource removes active visibility for skills that were absent
// from a successfully scanned repository tree. Historical revisions remain
// addressable for lock restoration.
func (s *Store) MarkMissingFromSource(ctx context.Context, repo Repository, presentPaths map[string]struct{}) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("catalogue database is required")
	}
	repoKey := repo.OrganizationID + "/" + repo.ID
	rows, err := s.DB.QueryContext(ctx, `SELECT id, relative_path, active_revision_id FROM skills WHERE organization_id=? AND repository_id=? AND active_revision_id IS NOT NULL`, repo.OrganizationID, repoKey)
	if err != nil {
		return err
	}
	defer rows.Close()
	type missingSkill struct{ id, path, revision string }
	var missing []missingSkill
	for rows.Next() {
		var item missingSkill
		if err := rows.Scan(&item.id, &item.path, &item.revision); err != nil {
			return err
		}
		if _, ok := presentPaths[item.path]; !ok {
			missing = append(missing, item)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range missing {
		if _, err := tx.ExecContext(ctx, `UPDATE skill_revisions SET state='removed_from_source' WHERE id=? AND state='active'`, item.revision); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET active_revision_id=NULL WHERE id=?`, item.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events(organization_id, event_type, details_json, occurred_at) VALUES (?, 'active_revision_changed', ?, ?)`, repo.OrganizationID, `{"skill_id":"`+item.id+`","reason":"removed_from_source"}`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RoutingDocuments(ctx context.Context, organizationID string, searchableMetadataKeys ...[]string) ([]search.Document, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.name, r.description, r.compatibility, r.metadata_json, sk.searchable, sk.repository_id, sk.relative_path, r.commit_sha, r.tree_sha, repo.trust_level, r.has_scripts FROM skill_revisions r JOIN skills sk ON sk.active_revision_id=r.id JOIN repositories repo ON repo.id=sk.repository_id WHERE sk.organization_id=? AND r.state='active' AND sk.searchable=1`, organizationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var docs []search.Document
	for rows.Next() {
		var id, name, description, compatibility, metadata string
		var searchable, hasScripts int
		var repositoryID, path, commit, tree, trustLevel string
		if err := rows.Scan(&id, &name, &description, &compatibility, &metadata, &searchable, &repositoryID, &path, &commit, &tree, &trustLevel, &hasScripts); err != nil {
			return nil, err
		}
		var values map[string]string
		if err := json.Unmarshal([]byte(metadata), &values); err != nil {
			values = map[string]string{}
		}
		if len(searchableMetadataKeys) > 0 {
			allowed := make(map[string]struct{}, len(searchableMetadataKeys[0]))
			for _, key := range searchableMetadataKeys[0] {
				allowed[key] = struct{}{}
			}
			filtered := make(map[string]string, len(allowed))
			for key, value := range values {
				if _, ok := allowed[key]; ok {
					filtered[key] = value
				}
			}
			values = filtered
		}
		docs = append(docs, search.Document{ID: id, OrganizationID: organizationID, Name: name, Description: description, Compatibility: compatibility, Metadata: values, Searchable: searchable == 1, RepositoryID: strings.TrimPrefix(repositoryID, organizationID+"/"), Path: path, Commit: commit, Tree: tree, TrustLevel: trustLevel, HasScripts: hasScripts == 1})
	}
	return docs, rows.Err()
}
func (s *Store) Revision(ctx context.Context, organizationID, revisionID string) (RevisionInfo, error) {
	var info RevisionInfo
	err := s.DB.QueryRowContext(ctx, `SELECT r.id, r.skill_id, r.name, r.license, r.compatibility, r.allowed_tools, sk.repository_id, repo.url, sk.relative_path, r.commit_sha, r.tree_sha, r.archive_sha256_tar_gz, r.archive_sha256_zip FROM skill_revisions r JOIN skills sk ON sk.id=r.skill_id JOIN repositories repo ON repo.id=sk.repository_id WHERE r.id=? AND sk.organization_id=? AND r.state IN ('active','superseded','removed_from_source')`, revisionID, organizationID).Scan(&info.RevisionID, &info.SkillID, &info.Name, &info.License, &info.Compatibility, &info.AllowedTools, &info.RepositoryID, &info.RepositoryURL, &info.Path, &info.Commit, &info.Tree, &info.ArchiveSHA256TarGZ, &info.ArchiveSHA256ZIP)
	return info, err
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
