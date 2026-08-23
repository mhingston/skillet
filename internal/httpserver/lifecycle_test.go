package httpserver

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

func TestLifecycleToolRecordsObservedEvent(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(t.TempDir(), "packages"))
	tarDigest, _ := packages.Put("tar.gz", []byte("tar"))
	zipDigest, _ := packages.Put("zip", []byte("zip"))
	catalog := catalogue.New(db, packages)
	skill := discovery.Skill{RelativePath: "plan", State: discovery.Admitted, Searchable: true, Frontmatter: skillspec.Frontmatter{Name: "plan", Description: "plan"}}
	rev, err := catalog.Admit(ctx, catalogue.Repository{ID: "skills", OrganizationID: "demo", URL: "https://example.com", Ref: "main"}, skill, "commit1", "tree1", catalogue.PackageDigests{TarGZ: tarDigest, ZIP: zipDigest})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{catalogue: catalog, organizationID: "demo", metrics: &Metrics{}}
	_, out, err := s.lifecycleTool(ctx, nil, lifecycleInput{
		Lifecycle:     lifecycleReference{RevisionID: rev.ID, SkillID: rev.SkillID, Commit: "commit1", Tree: "tree1", ArchiveSHA256: tarDigest},
		Event:         "activated",
		CorrelationID: "run-1",
		Source:        "pi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "recorded" || out.RevisionID != rev.ID || out.Event != "activated" {
		t.Fatalf("out = %+v", out)
	}
}

func TestLifecycleToolRejectsOversizedCorrelation(t *testing.T) {
	s := &Server{catalogue: &catalogue.Store{}, organizationID: "demo", metrics: &Metrics{}}
	_, _, err := s.lifecycleTool(context.Background(), nil, lifecycleInput{CorrelationID: string(make([]byte, 129)), Event: "activated"})
	if err == nil {
		t.Fatal("oversized correlation accepted")
	}
}
