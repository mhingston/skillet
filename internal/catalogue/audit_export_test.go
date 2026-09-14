package catalogue

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mhingston/skillet/internal/auditexport"
	"github.com/mhingston/skillet/internal/discovery"
	"github.com/mhingston/skillet/internal/skillspec"
	"github.com/mhingston/skillet/internal/store"
)

type failingAuditSink struct{}

func (failingAuditSink) Export(context.Context, auditexport.Event) error {
	return errors.New("sink unavailable")
}

func TestAuditExporterFailureCannotRollbackAuthoritativeState(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "catalogue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	catalog := New(db)
	exporter := auditexport.New(failingAuditSink{}, nil)
	catalog.ConfigureAuditExporter(exporter)

	skill := discovery.Skill{
		RelativePath: "bad",
		State:        discovery.Quarantined,
		Version:      "01.2.3",
		Frontmatter: skillspec.Frontmatter{
			Name:        "bad",
			Description: "Bad",
			Metadata:    map[string]string{"version": "01.2.3", "source": "fixture"},
		},
		Findings: []skillspec.Finding{{Code: skillspec.FindingInvalidVersion, Message: "invalid"}},
	}

	if err := catalog.RecordQuarantine(ctx, Repository{ID: "skills", OrganizationID: "demo"}, skill, "commit", "tree"); err != nil {
		t.Fatalf("authoritative operation failed because exporter failed: %v", err)
	}
	if exporter.Attempts() != 1 || exporter.Failures() != 1 {
		t.Fatalf("export attempts=%d failures=%d", exporter.Attempts(), exporter.Failures())
	}

	var state string
	if err := db.QueryRowContext(ctx, `SELECT state FROM skill_revisions WHERE id=?`, RevisionID("demo", "skills", "bad", "commit", "tree")).Scan(&state); err != nil {
		t.Fatalf("committed revision missing: %v", err)
	}
	if state != "quarantined" {
		t.Fatalf("revision state = %q", state)
	}

	var event string
	if err := db.QueryRowContext(ctx, `SELECT event_type FROM audit_events WHERE organization_id=? ORDER BY id DESC LIMIT 1`, "demo").Scan(&event); err != nil {
		t.Fatalf("authoritative audit missing: %v", err)
	}
	if event != "skill_quarantined" {
		t.Fatalf("authoritative event = %q", event)
	}
}
