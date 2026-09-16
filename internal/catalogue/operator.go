package catalogue

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mhingston/skillet/internal/skillspec"
)

// OperatorRepositoryStatus is a bounded operational projection. Source and
// startup configuration remain authoritative and are deliberately read-only.
type OperatorRepositoryStatus struct {
	ID                     string
	URL                    string
	Ref                    string
	TrustLevel             string
	Owner                  string
	ActiveSkills           int
	QuarantinedRevisions   int
	CurrentQuarantined     int
	CurrentQuarantineKnown bool
	CurrentSnapshotCommit  string
	LastReconcileEvent     string
	LastReconciledAt       string
}

type OperatorFinding struct {
	Code    string
	Message string
}

type OperatorQuarantine struct {
	RevisionID   string
	SkillID      string
	RepositoryID string
	Path         string
	Name         string
	Commit       string
	Tree         string
	Findings     []OperatorFinding
}

type OperatorAuditEvent struct {
	EventType    string
	ActorType    string
	ActorID      string
	RepositoryID string
	SkillID      string
	RevisionID   string
	RequestID    string
	OccurredAt   string
}

type OperatorSnapshot struct {
	Repositories          []OperatorRepositoryStatus
	CurrentQuarantined    []OperatorQuarantine
	HistoricalQuarantined []OperatorQuarantine
	AuditEvents           []OperatorAuditEvent
	FeedbackCount         int
	LifecycleCount        int
}

type AuditExportStatus struct {
	Enabled  bool
	Attempts uint64
	Failures uint64
}

// OperatorSnapshot returns only organization-scoped operational metadata. It
// never exposes arbitrary audit detail payloads, authentication claims, source
// file bodies, or configuration secrets.
func (s *Store) OperatorSnapshot(ctx context.Context, organizationID string, auditLimit int) (OperatorSnapshot, error) {
	if s == nil || s.DB == nil || strings.TrimSpace(organizationID) == "" {
		return OperatorSnapshot{}, fmt.Errorf("catalogue database and organization are required")
	}
	if auditLimit == 0 {
		auditLimit = 50
	}
	if auditLimit < 1 || auditLimit > 100 {
		return OperatorSnapshot{}, fmt.Errorf("audit limit must be between 1 and 100")
	}

	snapshot := OperatorSnapshot{}
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id, r.url, r.tracked_ref, r.trust_level, r.owner,
		(SELECT COUNT(*) FROM skills sk WHERE sk.organization_id=r.organization_id AND sk.repository_id=r.id AND sk.active_revision_id IS NOT NULL),
		(SELECT COUNT(*) FROM skill_revisions sr JOIN skills sk ON sk.id=sr.skill_id WHERE sk.organization_id=r.organization_id AND sk.repository_id=r.id AND sr.state='quarantined'),
		COALESCE((SELECT ae.event_type FROM audit_events ae WHERE ae.organization_id=r.organization_id AND ae.repository_id=r.id AND ae.event_type IN ('repository_sync_succeeded','repository_sync_failed') ORDER BY ae.rowid DESC LIMIT 1), ''),
		COALESCE((SELECT ae.occurred_at FROM audit_events ae WHERE ae.organization_id=r.organization_id AND ae.repository_id=r.id AND ae.event_type IN ('repository_sync_succeeded','repository_sync_failed') ORDER BY ae.rowid DESC LIMIT 1), ''),
		COALESCE((SELECT ae.details_json FROM audit_events ae WHERE ae.organization_id=r.organization_id AND ae.repository_id=r.id AND ae.event_type IN ('repository_sync_succeeded','repository_sync_failed') ORDER BY ae.rowid DESC LIMIT 1), '{}')
		FROM repositories r WHERE r.organization_id=? ORDER BY r.id`, organizationID)
	if err != nil {
		return OperatorSnapshot{}, err
	}
	for rows.Next() {
		var status OperatorRepositoryStatus
		var lastReconcileDetails string
		if err := rows.Scan(&status.ID, &status.URL, &status.Ref, &status.TrustLevel, &status.Owner, &status.ActiveSkills, &status.QuarantinedRevisions, &status.LastReconcileEvent, &status.LastReconciledAt, &lastReconcileDetails); err != nil {
			rows.Close()
			return OperatorSnapshot{}, err
		}
		status.URL = redactOperatorURL(status.URL)
		if status.LastReconcileEvent == "repository_sync_succeeded" {
			var details struct {
				Commit string `json:"commit"`
			}
			if err := json.Unmarshal([]byte(lastReconcileDetails), &details); err == nil && strings.TrimSpace(details.Commit) != "" {
				status.CurrentSnapshotCommit = details.Commit
				status.CurrentQuarantineKnown = true
			}
		}
		snapshot.Repositories = append(snapshot.Repositories, status)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return OperatorSnapshot{}, err
	}
	rows.Close()

	for i := range snapshot.Repositories {
		repository := &snapshot.Repositories[i]
		if !repository.CurrentQuarantineKnown {
			continue
		}
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_revisions sr JOIN skills sk ON sk.id=sr.skill_id WHERE sk.organization_id=? AND sk.repository_id=? AND sr.state='quarantined' AND sr.commit_sha=?`, organizationID, repository.ID, repository.CurrentSnapshotCommit).Scan(&repository.CurrentQuarantined); err != nil {
			return OperatorSnapshot{}, err
		}
	}

	quarantineRows, err := s.DB.QueryContext(ctx, `SELECT sr.id, sr.skill_id, sk.repository_id, sk.relative_path, sr.name, sr.commit_sha, sr.tree_sha, sr.validation_result_json
		FROM skill_revisions sr JOIN skills sk ON sk.id=sr.skill_id
		WHERE sk.organization_id=? AND sr.state='quarantined'
		ORDER BY sr.rowid DESC LIMIT 50`, organizationID)
	if err != nil {
		return OperatorSnapshot{}, err
	}
	for quarantineRows.Next() {
		var item OperatorQuarantine
		var encodedFindings string
		if err := quarantineRows.Scan(&item.RevisionID, &item.SkillID, &item.RepositoryID, &item.Path, &item.Name, &item.Commit, &item.Tree, &encodedFindings); err != nil {
			quarantineRows.Close()
			return OperatorSnapshot{}, err
		}
		var findings []skillspec.Finding
		if err := json.Unmarshal([]byte(encodedFindings), &findings); err == nil {
			for _, finding := range findings {
				item.Findings = append(item.Findings, OperatorFinding{Code: string(finding.Code), Message: boundedOperatorText(finding.Message, 320)})
			}
		}
		snapshot.HistoricalQuarantined = append(snapshot.HistoricalQuarantined, item)
	}
	if err := quarantineRows.Err(); err != nil {
		quarantineRows.Close()
		return OperatorSnapshot{}, err
	}
	quarantineRows.Close()

	for _, repository := range snapshot.Repositories {
		if !repository.CurrentQuarantineKnown || repository.CurrentQuarantined == 0 {
			continue
		}
		currentRows, err := s.DB.QueryContext(ctx, `SELECT sr.id, sr.skill_id, sk.repository_id, sk.relative_path, sr.name, sr.commit_sha, sr.tree_sha, sr.validation_result_json
			FROM skill_revisions sr JOIN skills sk ON sk.id=sr.skill_id
			WHERE sk.organization_id=? AND sk.repository_id=? AND sr.state='quarantined' AND sr.commit_sha=?
			ORDER BY sr.rowid DESC LIMIT 50`, organizationID, repository.ID, repository.CurrentSnapshotCommit)
		if err != nil {
			return OperatorSnapshot{}, err
		}
		for currentRows.Next() {
			var item OperatorQuarantine
			var encodedFindings string
			if err := currentRows.Scan(&item.RevisionID, &item.SkillID, &item.RepositoryID, &item.Path, &item.Name, &item.Commit, &item.Tree, &encodedFindings); err != nil {
				currentRows.Close()
				return OperatorSnapshot{}, err
			}
			var findings []skillspec.Finding
			if err := json.Unmarshal([]byte(encodedFindings), &findings); err == nil {
				for _, finding := range findings {
					item.Findings = append(item.Findings, OperatorFinding{Code: string(finding.Code), Message: boundedOperatorText(finding.Message, 320)})
				}
			}
			snapshot.CurrentQuarantined = append(snapshot.CurrentQuarantined, item)
		}
		if err := currentRows.Err(); err != nil {
			currentRows.Close()
			return OperatorSnapshot{}, err
		}
		currentRows.Close()
	}

	auditRows, err := s.DB.QueryContext(ctx, `SELECT event_type, COALESCE(actor_type, ''), COALESCE(actor_id, ''), COALESCE(repository_id, ''), COALESCE(skill_id, ''), COALESCE(revision_id, ''), COALESCE(request_id, ''), occurred_at
		FROM audit_events WHERE organization_id=? ORDER BY rowid DESC LIMIT ?`, organizationID, auditLimit)
	if err != nil {
		return OperatorSnapshot{}, err
	}
	for auditRows.Next() {
		var event OperatorAuditEvent
		if err := auditRows.Scan(&event.EventType, &event.ActorType, &event.ActorID, &event.RepositoryID, &event.SkillID, &event.RevisionID, &event.RequestID, &event.OccurredAt); err != nil {
			auditRows.Close()
			return OperatorSnapshot{}, err
		}
		event.ActorID = boundedOperatorText(event.ActorID, 256)
		snapshot.AuditEvents = append(snapshot.AuditEvents, event)
	}
	if err := auditRows.Err(); err != nil {
		auditRows.Close()
		return OperatorSnapshot{}, err
	}
	auditRows.Close()

	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_feedback WHERE organization_id=?`, organizationID).Scan(&snapshot.FeedbackCount); err != nil {
		return OperatorSnapshot{}, err
	}
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events WHERE organization_id=? AND event_type IN ('skill_activated','skill_deactivated','skill_completed','skill_failed')`, organizationID).Scan(&snapshot.LifecycleCount); err != nil {
		return OperatorSnapshot{}, err
	}
	return snapshot, nil
}

// AuditExportStatus exposes only bounded exporter health. Destination details,
// credentials, and arbitrary event payloads remain outside the UI contract.
func (s *Store) AuditExportStatus() AuditExportStatus {
	if s == nil || s.auditExporter == nil {
		return AuditExportStatus{}
	}
	status := AuditExportStatus{Enabled: true}
	if counters, ok := s.auditExporter.(interface {
		Attempts() uint64
		Failures() uint64
	}); ok {
		status.Attempts = counters.Attempts()
		status.Failures = counters.Failures()
	}
	return status
}

func redactOperatorURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "[configured source]"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func boundedOperatorText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}
