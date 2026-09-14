package collaboration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/store"
)

func TestStoreThreadsWatchesModerationAndSignals(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO organizations(id) VALUES ('demo')`); err != nil {
		t.Fatal(err)
	}
	collab, err := New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	first, err := collab.PostComment(ctx, "demo", "demo/repo/plan", "alice", "rev-1", `<script>alert("x")</script> markdown **data**`)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionID != "rev-1" || first.BodySHA256 == "" {
		t.Fatalf("first comment provenance = %+v", first)
	}
	changed, err := collab.SetWatch(ctx, "demo", "demo/repo/plan", "alice", "rev-1", true)
	if err != nil || !changed {
		t.Fatalf("initial watch changed=%v err=%v", changed, err)
	}
	if changed, err := collab.SetWatch(ctx, "demo", "demo/repo/plan", "alice", "rev-2", true); err != nil || changed {
		t.Fatalf("repeat watch changed=%v err=%v", changed, err)
	}
	if activity, err := collab.Activity(ctx, "demo", "alice", 10); err != nil || len(activity) != 0 {
		t.Fatalf("historical activity = %+v err=%v", activity, err)
	}

	second, err := collab.PostComment(ctx, "demo", "demo/repo/plan", "bob", "rev-2", "new revision discussion")
	if err != nil {
		t.Fatal(err)
	}
	activity, err := collab.Activity(ctx, "demo", "alice", 10)
	if err != nil || len(activity) != 1 || activity[0].ID != second.ID || activity[0].WatchRevisionID != "rev-2" {
		t.Fatalf("activity = %+v err=%v", activity, err)
	}
	thread, err := collab.Thread(ctx, "demo", "demo/repo/plan", 10)
	if err != nil || len(thread) != 2 || thread[0].RevisionID != "rev-1" || thread[1].RevisionID != "rev-2" {
		t.Fatalf("thread = %+v err=%v", thread, err)
	}

	moderated, changed, err := collab.Moderate(ctx, "demo", "demo/repo/plan", first.ID, "maintainer", "unsafe content")
	if err != nil || !changed {
		t.Fatalf("moderate changed=%v err=%v", changed, err)
	}
	if moderated.State != CommentModerated || moderated.Body != "" || moderated.BodySHA256 == "" || moderated.RevisionID != "rev-1" || moderated.ModeratedBy != "maintainer" {
		t.Fatalf("moderated comment = %+v", moderated)
	}
	if _, changed, err := collab.Moderate(ctx, "demo", "demo/repo/plan", first.ID, "maintainer", "repeat"); err != nil || changed {
		t.Fatalf("repeat moderation changed=%v err=%v", changed, err)
	}

	if changed, err := collab.SetWatch(ctx, "demo", "demo/repo/plan", "alice", "", false); err != nil || !changed {
		t.Fatalf("unwatch changed=%v err=%v", changed, err)
	}
	watching, err := collab.IsWatching(ctx, "demo", "demo/repo/plan", "alice")
	if err != nil || watching {
		t.Fatalf("watching=%v err=%v", watching, err)
	}

	seedEvidenceFixture(t, ctx, db)
	signals, err := collab.EvidenceSignals(ctx, "demo", "demo/repo/plan", "rev-evidence")
	if err != nil {
		t.Fatal(err)
	}
	if !signals.Available || signals.Materialisations != 2 || signals.LifecycleCompleted != 1 || signals.LifecycleFailed != 2 || signals.EffectivePatterns != 2 || signals.WorkaroundCorrections != 3 || signals.ImprovementSuggested != 1 {
		t.Fatalf("signals = %+v", signals)
	}
	missing, err := collab.EvidenceSignals(ctx, "demo", "mcp-tool:server/tool", "tool-rev")
	if err != nil || missing.Available {
		t.Fatalf("non-skill signals = %+v err=%v", missing, err)
	}
}

func seedEvidenceFixture(t *testing.T, ctx context.Context, db execer) {
	t.Helper()
	statements := []string{
		`INSERT INTO repositories(id, organization_id, url, tracked_ref, trust_level, owner) VALUES ('demo/repo','demo','https://example.invalid/repo','main','approved','team')`,
		`INSERT INTO skills(id, organization_id, repository_id, relative_path, active_revision_id, searchable, name, owner) VALUES ('demo/repo/plan','demo','demo/repo','plan','rev-evidence',1,'plan','team')`,
		`INSERT INTO skill_revisions(id, skill_id, commit_sha, tree_sha, archive_sha256_tar_gz, state, validation_result_json, license, compatibility, allowed_tools, name, description, metadata_json, archive_sha256_zip, admitted_at, has_scripts) VALUES ('rev-evidence','demo/repo/plan','commit','tree','tar','active','{}','','','','plan','plan','{}','zip','now',0)`,
		`INSERT INTO audit_events(organization_id,event_type,skill_id,revision_id) VALUES ('demo','materialisation_prepared','demo/repo/plan','rev-evidence'),('demo','materialisation_prepared','demo/repo/plan','rev-evidence'),('demo','skill_completed','demo/repo/plan','rev-evidence'),('demo','skill_failed','demo/repo/plan','rev-evidence'),('demo','skill_failed','demo/repo/plan','rev-evidence')`,
		`INSERT INTO skill_feedback(organization_id,skill_id,revision_id,archive_sha256,materialization_id,category,summary) VALUES ('demo','demo/repo/plan','rev-evidence','tar','m1','effective_pattern','a'),('demo','demo/repo/plan','rev-evidence','tar','m2','effective_pattern','b'),('demo','demo/repo/plan','rev-evidence','tar','m3','workaround_required','c'),('demo','demo/repo/plan','rev-evidence','tar','m4','user_correction','d'),('demo','demo/repo/plan','rev-evidence','tar','m5','user_correction','e'),('demo','demo/repo/plan','rev-evidence','tar','m6','improvement_suggested','f')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}
