package catalogue

import (
	"context"
	"fmt"
)

// RevisionByArchiveDigest resolves one retained immutable revision from a CAS
// package digest inside an organization. Digest-only package tokens predate
// claims authorization, so ambiguous provenance must fail closed rather than
// guessing which resource scope should authorize the bytes.
func (s *Store) RevisionByArchiveDigest(ctx context.Context, organizationID, digest string) (RevisionInfo, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.skill_id, r.name, COALESCE(r.version, ''), r.license, r.compatibility, r.allowed_tools, sk.repository_id, repo.url, sk.relative_path, r.commit_sha, r.tree_sha, r.archive_sha256_tar_gz, r.archive_sha256_zip FROM skill_revisions r JOIN skills sk ON sk.id=r.skill_id JOIN repositories repo ON repo.id=sk.repository_id WHERE sk.organization_id=? AND (r.archive_sha256_tar_gz=? OR r.archive_sha256_zip=?) AND r.state IN ('active','superseded','removed_from_source') LIMIT 2`, organizationID, digest, digest)
	if err != nil {
		return RevisionInfo{}, err
	}
	defer rows.Close()
	var matches []RevisionInfo
	for rows.Next() {
		var info RevisionInfo
		if err := rows.Scan(&info.RevisionID, &info.SkillID, &info.Name, &info.Version, &info.License, &info.Compatibility, &info.AllowedTools, &info.RepositoryID, &info.RepositoryURL, &info.Path, &info.Commit, &info.Tree, &info.ArchiveSHA256TarGZ, &info.ArchiveSHA256ZIP); err != nil {
			return RevisionInfo{}, err
		}
		matches = append(matches, info)
	}
	if err := rows.Err(); err != nil {
		return RevisionInfo{}, err
	}
	if len(matches) != 1 {
		return RevisionInfo{}, fmt.Errorf("package digest resolves to %d retained revisions", len(matches))
	}
	return matches[0], nil
}
