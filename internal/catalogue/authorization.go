package catalogue

import "context"

// RevisionByArchiveDigest resolves one retained immutable revision from a CAS
// package digest inside an organization. Both tar.gz and ZIP digests are
// accepted so a signed package URL can be re-authorized before download.
func (s *Store) RevisionByArchiveDigest(ctx context.Context, organizationID, digest string) (RevisionInfo, error) {
	var info RevisionInfo
	err := s.DB.QueryRowContext(ctx, `SELECT r.id, r.skill_id, r.name, COALESCE(r.version, ''), r.license, r.compatibility, r.allowed_tools, sk.repository_id, repo.url, sk.relative_path, r.commit_sha, r.tree_sha, r.archive_sha256_tar_gz, r.archive_sha256_zip FROM skill_revisions r JOIN skills sk ON sk.id=r.skill_id JOIN repositories repo ON repo.id=sk.repository_id WHERE sk.organization_id=? AND (r.archive_sha256_tar_gz=? OR r.archive_sha256_zip=?) AND r.state IN ('active','superseded','removed_from_source') LIMIT 1`, organizationID, digest, digest).Scan(&info.RevisionID, &info.SkillID, &info.Name, &info.Version, &info.License, &info.Compatibility, &info.AllowedTools, &info.RepositoryID, &info.RepositoryURL, &info.Path, &info.Commit, &info.Tree, &info.ArchiveSHA256TarGZ, &info.ArchiveSHA256ZIP)
	return info, err
}
