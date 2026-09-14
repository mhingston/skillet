package catalogue

import (
	"context"
	"time"

	"github.com/mhingston/skillet/internal/auditexport"
)

// AuditExporter is intentionally transport-neutral. The concrete exporter is
// configured outside the catalogue and receives only a bounded audit envelope.
type AuditExporter interface {
	Export(context.Context, auditexport.Event)
}

type pendingAudit struct {
	organizationID string
	eventType      string
	occurredAt     time.Time
	actorType      string
	actorID        string
	repositoryID   string
	skillID        string
	revisionID     string
	requestID      string
}

func (s *Store) ConfigureAuditExporter(exporter AuditExporter) {
	if s == nil {
		return
	}
	s.auditExporter = exporter
}

func (s *Store) exportAudit(ctx context.Context, pending pendingAudit) {
	if s == nil || s.auditExporter == nil {
		return
	}
	s.auditExporter.Export(context.WithoutCancel(ctx), auditexport.NewEvent(
		pending.organizationID,
		pending.eventType,
		pending.occurredAt,
		auditexport.Metadata{
			ActorType:    pending.actorType,
			ActorID:      pending.actorID,
			RepositoryID: pending.repositoryID,
			SkillID:      pending.skillID,
			RevisionID:   pending.revisionID,
			RequestID:    pending.requestID,
		},
	))
}

func (s *Store) exportAudits(ctx context.Context, pending []pendingAudit) {
	for _, event := range pending {
		s.exportAudit(ctx, event)
	}
}
