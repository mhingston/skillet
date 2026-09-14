package httpserver

import (
	"context"
	"errors"
	"testing"

	authn "github.com/mhingston/skillet/internal/auth"
	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/search"
)

func TestClaimsAuthorizationFiltersLegacySearchAfterRanking(t *testing.T) {
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range []search.Document{
		{ID: "rev-a", SkillID: "demo/repo-a/release", OrganizationID: "demo", RepositoryID: "repo-a", Name: "release-a", Description: "database migration release checklist", Searchable: true, TrustLevel: "approved"},
		{ID: "rev-b", SkillID: "demo/repo-b/release", OrganizationID: "demo", RepositoryID: "repo-b", Name: "release-b", Description: "database release rollback checklist", Searchable: true, TrustLevel: "approved"},
	} {
		if err := index.Add(doc); err != nil {
			t.Fatal(err)
		}
	}
	s := NewWithSearch(nil, nil, index, "demo", candidate.Signer{Key: []byte("candidate-key")})
	s.ConfigureSearch(50, 50, 20, 60, 10)
	identity := authn.Identity{Subject: "user-1", OrganizationID: "demo", Permissions: map[string]struct{}{"capability.reader": {}}}
	ctx := withAuthenticatedIdentity(context.Background(), identity)
	input := searchInput{Query: "database release checklist", Limit: 10}

	_, baseline, err := s.searchTool(ctx, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Candidates) != 2 {
		t.Fatalf("baseline candidates = %d, want 2", len(baseline.Candidates))
	}
	allowed := baseline.Candidates[1]
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"capability.reader"},
		Actions:     []authz.Action{authz.ActionCapabilitySearch},
		Resources:   []authz.ResourceRule{{IDs: []string{allowed.Skill.SkillID}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAuthorization(policy)

	_, filtered, err := s.authorizedSearchTool(ctx, nil, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Candidates) != 1 || filtered.Candidates[0].Skill.SkillID != allowed.Skill.SkillID {
		t.Fatalf("filtered candidates = %+v", filtered.Candidates)
	}
	got, want := filtered.Candidates[0].Ranking, allowed.Ranking
	if got.Rank != want.Rank || got.Score != want.Score || got.LexicalRank != want.LexicalRank || got.VectorRank != want.VectorRank {
		t.Fatalf("authorization changed ranking evidence: got=%+v want=%+v", got, want)
	}

	missingPermission := withAuthenticatedIdentity(context.Background(), authn.Identity{Subject: "user-2", OrganizationID: "demo"})
	if _, _, err := s.authorizedSearchTool(missingPermission, nil, input); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("missing permission error = %v, want authorization denial", err)
	}
}

func TestLockedRestoreAuthorizationRunsBeforePackageAccess(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	policy, err := authz.NewClaimsPolicy([]authz.Grant{{
		Permissions: []string{"capability.materializer"},
		Actions:     []authz.Action{authz.ActionCapabilityMaterialize},
		Resources:   []authz.ResourceRule{{IDs: []string{"some-other-skill"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureAuthorization(policy)
	// ResolveRevision uses catalogue provenance only. If authorization were
	// applied after restore/package access, this nil package store would produce
	// a package error instead of the expected authorization denial.
	s.restorer.Packages = nil
	ctx := withAuthenticatedIdentity(context.Background(), authn.Identity{
		Subject:        "user-1",
		OrganizationID: "demo",
		Permissions:    map[string]struct{}{"capability.materializer": {}},
	})
	input := materializeInput{Locked: &lockedInput{
		SkillID:       first.SkillID,
		RepositoryID:  "skills",
		Path:          first.Path,
		Commit:        first.Commit,
		Tree:          first.Tree,
		ArchiveSHA256: first.ArchiveSHA256TarGZ,
		Format:        "tar.gz",
	}}
	if _, _, err := s.authorizedMaterializeTool(ctx, nil, input); !errors.Is(err, ErrAuthorizationDenied) {
		t.Fatalf("locked restore error = %v, want authorization denial before package access", err)
	}
}
