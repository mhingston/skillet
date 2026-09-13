package httpserver

import (
	"context"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/candidate"
	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/search"
)

func TestMaterializeRejectsYankedNewSelectionButAllowsExactLockedRestore(t *testing.T) {
	s, first, _ := lockedMaterializeFixture(t)
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Add(search.Document{
		ID:             first.RevisionID,
		SkillID:        first.SkillID,
		OrganizationID: "demo",
		RepositoryID:   "skills",
		Path:           first.Path,
		Commit:         first.Commit,
		Tree:           first.Tree,
		Name:           first.Name,
		Description:    "Withdrawn plan capability.",
		TrustLevel:     "approved",
		Searchable:     true,
		Metadata:       map[string]string{governance.StateKey: string(capability.StatusYanked)},
	}); err != nil {
		t.Fatal(err)
	}
	service, err := capability.New(index, nil)
	if err != nil {
		t.Fatal(err)
	}
	s.search = index
	s.ConfigureCapabilities(service)
	t.Cleanup(func() { s.ConfigureCapabilities(nil) })

	token, err := s.signer.Sign(candidate.Payload{
		Version: 1, OrganizationID: "demo", RevisionID: first.RevisionID, QueryID: "pre-yank-query",
		IssuedAt: time.Now().Add(-time.Second).Unix(), ExpiresAt: time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.materializeTool(context.Background(), nil, materializeInput{CandidateID: token}); err == nil {
		t.Fatal("previously issued candidate materialized after its revision was yanked")
	}

	_, restored, err := s.materializeTool(context.Background(), nil, materializeInput{Locked: &lockedInput{
		SkillID:       first.SkillID,
		RepositoryID:  first.RepositoryID,
		Path:          first.Path,
		Commit:        first.Commit,
		Tree:          first.Tree,
		ArchiveSHA256: first.ArchiveSHA256TarGZ,
		Format:        "tar.gz",
	}})
	if err != nil {
		t.Fatalf("exact locked restore of yanked revision failed: %v", err)
	}
	if restored.Lifecycle.RevisionID != first.RevisionID || restored.Package.ArchiveSHA256 != first.ArchiveSHA256TarGZ {
		t.Fatalf("locked restore changed immutable provenance: %+v", restored)
	}
}
