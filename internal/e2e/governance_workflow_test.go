package e2e

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/lockfile"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/mhingston/skillet/internal/restore"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

func TestOfflineCapabilityGovernanceAndExactYankedRestore(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)

	sourceRoot := filepath.Join(root, "central")
	writeGovernanceSkill(t, sourceRoot, "old", "Legacy release workflow", map[string]string{
		governance.DeprecatedKey: "true",
		governance.ReplacedByKey: "demo/central/new",
	})
	writeGovernanceSkill(t, sourceRoot, "new", "Supported release workflow", nil)
	writeGovernanceSkill(t, sourceRoot, "yanked", "Withdrawn release workflow", map[string]string{
		governance.StateKey:  string(capability.StatusYanked),
		governance.ReasonKey: "withdrawn by maintainer",
	})

	source, err := gitstore.NewLocalSource(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := source.Fetch(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	repo := catalogue.Repository{ID: "central", OrganizationID: "demo", URL: "file://" + filepath.ToSlash(sourceRoot), Ref: "local", TrustLevel: "approved", Owner: "platform-team"}
	result, err := ingest.SyncAtCommitWithOptions(ctx, source, repo, packages, catalog, commit, ingest.Options{Include: []string{"**/SKILL.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Admitted != 3 || result.Quarantined != 0 {
		t.Fatalf("governance fixture admission = %+v", result)
	}

	docs, err := catalog.RoutingDocuments(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	index, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := index.Rebuild(docs); err != nil {
		t.Fatal(err)
	}
	scope := mustCapabilityScope(t, "demo", "", "")
	service, err := capability.New(index, []capability.SourcePolicy{{
		RepositoryID: "central",
		Scope:        scope,
		Owner:        "platform-team",
	}})
	if err != nil {
		t.Fatal(err)
	}

	results, _, err := service.Search("release workflow", 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	seenOld, seenNew, seenYanked := false, false, false
	var oldRevision, newRevision string
	for _, candidate := range results {
		switch candidate.Capability.Identity.ID {
		case "demo/central/old":
			seenOld = true
			oldRevision = candidate.Capability.Provenance.RevisionID
			if candidate.Capability.Status != capability.StatusDeprecated || candidate.Capability.Governance.ReplacedBy != "demo/central/new" || !candidate.Capability.Governance.ReplacementResolved {
				t.Fatalf("deprecated candidate = %+v", candidate.Capability)
			}
			if candidate.Capability.Governance.Owner != "platform-team" {
				t.Fatalf("owner projection = %+v", candidate.Capability.Governance)
			}
		case "demo/central/new":
			seenNew = true
			newRevision = candidate.Capability.Provenance.RevisionID
		case "demo/central/yanked":
			seenYanked = true
		}
	}
	if !seenOld || !seenNew || seenYanked {
		t.Fatalf("governed discovery old=%t new=%t yanked=%t results=%+v", seenOld, seenNew, seenYanked, results)
	}
	if err := service.AllowsNewSelection(oldRevision); err != nil {
		t.Fatalf("deprecated revision should remain explicitly selectable: %v", err)
	}
	if err := service.AllowsNewSelection(newRevision); err != nil {
		t.Fatalf("active revision should be selectable: %v", err)
	}

	// Ownership is presentation/control metadata. Re-projecting the same routing
	// documents under another owner must not change semantic order or scores.
	otherOwner, err := capability.New(index, []capability.SourcePolicy{{RepositoryID: "central", Scope: scope, Owner: "another-team"}})
	if err != nil {
		t.Fatal(err)
	}
	otherResults, _, err := otherOwner.Search("release workflow", 50, 50, 10, 60, scope, search.Filters{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(otherResults) {
		t.Fatalf("ownership changed result count: %d vs %d", len(results), len(otherResults))
	}
	for i := range results {
		if results[i].Capability.Provenance.RevisionID != otherResults[i].Capability.Provenance.RevisionID || results[i].Ranking.Rank != otherResults[i].Ranking.Rank || results[i].Ranking.Score != otherResults[i].Ranking.Score {
			t.Fatalf("ownership changed routing at %d: %+v vs %+v", i, results[i], otherResults[i])
		}
	}

	var yankedDoc search.Document
	for _, doc := range docs {
		if doc.SkillID == "demo/central/yanked" {
			yankedDoc = doc
			break
		}
	}
	if yankedDoc.ID == "" {
		t.Fatal("yanked immutable revision missing from catalogue routing snapshot")
	}
	if err := service.AllowsNewSelection(yankedDoc.ID); err == nil {
		t.Fatal("yanked revision unexpectedly allowed for new selection")
	}
	info, err := catalog.Revision(ctx, "demo", yankedDoc.ID)
	if err != nil {
		t.Fatal(err)
	}
	restorer := &restore.Restorer{
		OrganizationID: "demo",
		Catalogue:      catalog,
		Packages:       packages,
		PackageSigner:  packageurl.Signer{Key: []byte("governance-package-key")},
		PublicBaseURL:  "https://skillet.example",
	}
	locked := lockfile.Entry{
		Name: info.Name,
		Source: lockfile.Source{
			Type:          "local",
			RepositoryID:  info.RepositoryID,
			RepositoryURL: info.RepositoryURL,
			Path:          info.Path,
		},
		Resolved:  lockfile.Resolved{Commit: info.Commit, Tree: info.Tree},
		Integrity: lockfile.Integrity{Algorithm: "sha256", Archive: info.ArchiveSHA256TarGZ, Format: "tar.gz"},
	}
	restored, err := restorer.Restore(ctx, lockfile.File{LockfileVersion: 1, Skills: map[string]lockfile.Entry{info.SkillID: locked}})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].Revision.RevisionID != yankedDoc.ID || restored[0].Digest != info.ArchiveSHA256TarGZ || restored[0].Revision.Commit != info.Commit || restored[0].Revision.Tree != info.Tree {
		t.Fatalf("exact yanked restore = %+v", restored)
	}
}

func writeGovernanceSkill(t *testing.T, sourceRoot, name, description string, metadata map[string]string) {
	t.Helper()
	dir := filepath.Join(sourceRoot, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n"
	if len(metadata) > 0 {
		body += "metadata:\n"
		for _, key := range []string{governance.StateKey, governance.ReasonKey, governance.DeprecatedKey, governance.ReplacedByKey} {
			if value, ok := metadata[key]; ok {
				body += "  " + key + ": \"" + value + "\"\n"
			}
		}
	}
	body += "---\n# " + name + "\n\nDeterministic governance fixture.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
