package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/gitstore"
	"github.com/mhingston/skillet/internal/ingest"
	"github.com/mhingston/skillet/internal/packagebuilder"
	"github.com/mhingston/skillet/internal/packagestore"
	"github.com/mhingston/skillet/internal/search"
	"github.com/mhingston/skillet/internal/store"
)

const (
	externalCorpusURL      = "https://github.com/mhingston/agent-skills.git"
	externalCorpusOrg      = "external-test"
	externalCorpusRepo     = "mhingston-agent-skills"
	maxExternalTreeEntries = 20000
	maxExternalSkills      = 100
)

func TestExternalAgentSkillsCorpus(t *testing.T) {
	if got := runExternalCorpus(t); got != nil {
		t.Fatal(got)
	}
}

// runExternalCorpus is deliberately opt-in. An opted-in run is a real network
// integration test: network, Git, validation, package construction, SQLite
// admission, and routing search all have to succeed. A missing network must
// fail the opted-in test rather than being reported as a successful skip.
func runExternalCorpus(t *testing.T) error {
	if os.Getenv("SKILLET_EXTERNAL_CORPUS") != "1" {
		t.Skip("set SKILLET_EXTERNAL_CORPUS=1 to run the external corpus integration test")
	}

	url := os.Getenv("SKILLET_EXTERNAL_CORPUS_URL")
	if url == "" {
		url = externalCorpusURL
	}
	ref := os.Getenv("SKILLET_EXTERNAL_CORPUS_REF")
	if ref == "" {
		ref = "refs/heads/main"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	root := t.TempDir()
	mirror := gitstore.NewMirror(filepath.Join(root, "repositories", externalCorpusRepo+".git"))
	if err := mirror.Init(ctx, url); err != nil {
		return fmt.Errorf("initialize external corpus mirror: %w", err)
	}
	commit, err := mirror.Fetch(ctx, ref)
	if err != nil {
		return fmt.Errorf("fetch external corpus ref %q: %w", ref, err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(commit) {
		return fmt.Errorf("external corpus resolved non-commit identity %q", commit)
	}
	tree, err := mirror.ListTree(ctx, commit)
	if err != nil {
		return fmt.Errorf("list external corpus tree: %w", err)
	}
	if len(tree) > maxExternalTreeEntries {
		return fmt.Errorf("external corpus tree has %d entries, over bounded test limit %d", len(tree), maxExternalTreeEntries)
	}
	skillCount := 0
	for _, entry := range tree {
		if entry.Path == "SKILL.md" || strings.HasSuffix(entry.Path, "/SKILL.md") {
			skillCount++
		}
	}
	if skillCount == 0 || skillCount > maxExternalSkills {
		return fmt.Errorf("external corpus has %d skills, want 1..%d", skillCount, maxExternalSkills)
	}

	db, err := store.Open(ctx, filepath.Join(root, "catalogue.db"))
	if err != nil {
		return fmt.Errorf("open integration catalogue: %w", err)
	}
	defer db.Close()
	packages := packagestore.New(filepath.Join(root, "packages"))
	catalog := catalogue.New(db, packages)
	repo := catalogue.Repository{ID: externalCorpusRepo, OrganizationID: externalCorpusOrg, URL: url, Ref: ref, TrustLevel: "approved", Owner: "external-corpus-test"}
	result, err := ingest.SyncAtCommitWithOptions(ctx, mirror, repo, packages, catalog, commit, ingest.Options{
		Include:          []string{"**/SKILL.md"},
		Exclude:          []string{".git/**"},
		SearchExclusions: []discovery.MetadataRule{{Key: "mhingston.user-invocable", Equals: "false"}, {Key: "mhingston.internal", Equals: "true"}},
		PackageLimits:    packagebuilder.Limits{MaxFiles: 1000, MaxFileBytes: 25 << 20, MaxTotalBytes: 100 << 20, MaxArchiveBytes: 50 << 20},
	})
	if err != nil {
		return fmt.Errorf("admit external corpus at %s: %w", commit, err)
	}
	if result.Admitted == 0 || result.Admitted+result.Quarantined != skillCount {
		return fmt.Errorf("admission counts admitted=%d quarantined=%d discovered=%d", result.Admitted, result.Quarantined, skillCount)
	}

	docs, err := catalog.RoutingDocuments(ctx, externalCorpusOrg, []string{"tags", "category", "intent", "mhingston.user-invocable", "mhingston.internal"})
	if err != nil {
		return fmt.Errorf("load admitted routing documents: %w", err)
	}
	if len(docs) == 0 || len(docs) > result.Admitted {
		return fmt.Errorf("routing document count=%d, admitted=%d", len(docs), result.Admitted)
	}
	var plan search.Document
	foundPlan := false
	for _, doc := range docs {
		if doc.Name == "plan" {
			plan, foundPlan = doc, true
			break
		}
	}
	if !foundPlan {
		return fmt.Errorf("external corpus has no searchable plan skill")
	}

	index, err := search.New(nil)
	if err != nil {
		return fmt.Errorf("create external corpus search index: %w", err)
	}
	if err := index.Rebuild(docs); err != nil {
		return fmt.Errorf("index external corpus routing documents: %w", err)
	}
	hits, degraded, err := index.Search("create a detailed implementation plan", 50, 50, 5, 60)
	if err != nil {
		return fmt.Errorf("search external corpus: %w", err)
	}
	if !degraded {
		return fmt.Errorf("expected explicit degraded=true without a configured embedding provider")
	}
	if len(hits) == 0 || hits[0].ID != plan.ID {
		return fmt.Errorf("plan was not the top lexical candidate: hits=%+v plan=%s", hits, plan.ID)
	}

	revision, err := catalog.Revision(ctx, externalCorpusOrg, plan.ID)
	if err != nil {
		return fmt.Errorf("resolve admitted plan revision: %w", err)
	}
	for _, pkg := range []struct{ format, digest string }{{"tar.gz", revision.ArchiveSHA256TarGZ}, {"zip", revision.ArchiveSHA256ZIP}} {
		contents, getErr := packages.Get(pkg.format, pkg.digest)
		if getErr != nil {
			return fmt.Errorf("retrieve retained %s package: %w", pkg.format, getErr)
		}
		if len(contents) == 0 {
			return fmt.Errorf("retained %s package is empty", pkg.format)
		}
	}
	return nil
}
