// Package capability owns Skillet's task-capability discovery domain.
//
// Capability metadata is deliberately compact. Full package contents remain
// behind the existing explicit materialisation boundary.
package capability

import (
	"fmt"
	"strings"
	"unicode"
)

type Kind string

const (
	KindSkill    Kind = "skill"
	KindPlaybook Kind = "playbook"
)

type Status string

const (
	StatusActive     Status = "active"
	StatusDeprecated Status = "deprecated"
	StatusYanked     Status = "yanked"
)

// Scope is policy data, not routing text. An empty namespace/repository is
// organisation-wide (central). A repository-local capability is visible only
// in the exact validated repository context.
type Scope struct {
	Organization string `json:"organization"`
	Namespace    string `json:"namespace,omitempty"`
	Repository   string `json:"repository,omitempty"`
}

func NewScope(organization, namespace, repository string) (Scope, error) {
	s := Scope{
		Organization: strings.TrimSpace(organization),
		Namespace:    strings.TrimSpace(namespace),
		Repository:   strings.TrimSpace(repository),
	}
	if err := s.Validate(); err != nil {
		return Scope{}, err
	}
	return s, nil
}

func (s Scope) Validate() error {
	if err := validateScopePart("organization", s.Organization, 128, false); err != nil {
		return err
	}
	if err := validateScopePart("namespace", s.Namespace, 128, true); err != nil {
		return err
	}
	if err := validateScopePart("repository", s.Repository, 256, true); err != nil {
		return err
	}
	return nil
}

func validateScopePart(name, value string, max int, optional bool) error {
	if value == "" {
		if optional {
			return nil
		}
		return fmt.Errorf("scope %s is required", name)
	}
	if value != strings.TrimSpace(value) || len(value) > max || strings.Contains(value, "\\") || strings.Contains(value, "..") || strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("invalid scope %s", name)
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("invalid scope %s", name)
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-_.:/", r)) {
			return fmt.Errorf("invalid scope %s", name)
		}
	}
	return nil
}

// Allows reports whether this capability scope is visible in request. Scope
// only constrains eligibility; it never boosts ranking.
func (s Scope) Allows(request Scope) bool {
	if s.Organization == "" || request.Organization == "" || s.Organization != request.Organization {
		return false
	}
	if s.Namespace != "" && s.Namespace != request.Namespace {
		return false
	}
	if s.Repository != "" && s.Repository != request.Repository {
		return false
	}
	return true
}

type Identity struct {
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
}

type Source struct {
	RepositoryID string `json:"repository_id"`
	URL          string `json:"url,omitempty"`
	Path         string `json:"path,omitempty"`
}

type Provenance struct {
	RevisionID         string `json:"revision_id"`
	Commit             string `json:"commit"`
	Tree               string `json:"tree"`
	ArchiveSHA256TarGZ string `json:"archive_sha256_tar_gz,omitempty"`
	ArchiveSHA256ZIP   string `json:"archive_sha256_zip,omitempty"`
}

type Descriptor struct {
	Identity      Identity          `json:"identity"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Version       string            `json:"version,omitempty"`
	Compatibility string            `json:"compatibility,omitempty"`
	Scope         Scope             `json:"scope"`
	Source        Source            `json:"source"`
	Provenance    Provenance        `json:"provenance"`
	TrustLevel    string            `json:"trust_level,omitempty"`
	Status        Status            `json:"status"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	HasScripts    bool              `json:"has_scripts"`
}

type PackageDigests struct {
	TarGZ string `json:"tar_gz,omitempty"`
	ZIP   string `json:"zip,omitempty"`
}

type Detail struct {
	Descriptor      Descriptor     `json:"capability"`
	PackageDigests  PackageDigests `json:"package_digests,omitempty"`
	MaterializeWith string         `json:"materialize_with,omitempty"`
}

// SourcePolicy binds a source repository to visibility scope. RepositoryID is
// the catalogue/search repository id (without the organisation prefix).
type SourcePolicy struct {
	RepositoryID string
	Scope        Scope
}
