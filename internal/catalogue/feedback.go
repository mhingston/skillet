package catalogue

import (
	"context"
	"fmt"
	"strings"
)

type FeedbackObservation struct {
	Reference     MaterializationReference
	Category      string
	Summary       string
	CorrelationID string
	Source        string
}

type FeedbackRecord struct {
	ID                int64  `json:"id"`
	SkillID           string `json:"skill_id"`
	RevisionID        string `json:"revision_id"`
	ArchiveSHA256     string `json:"archive_sha256"`
	MaterializationID string `json:"materialization_id"`
	Category          string `json:"category"`
	Summary           string `json:"summary"`
	CorrelationID     string `json:"correlation_id,omitempty"`
	Source            string `json:"source,omitempty"`
	CreatedAt         string `json:"created_at"`
}

func (s *Store) RecordFeedback(ctx context.Context, organizationID string, observation FeedbackObservation) (FeedbackRecord, error) {
	if !validFeedbackCategory(observation.Category) {
		return FeedbackRecord{}, fmt.Errorf("unsupported feedback category %q", observation.Category)
	}
	provenance, err := s.ValidateMaterialization(ctx, organizationID, observation.Reference)
	if err != nil {
		return FeedbackRecord{}, err
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO skill_feedback(
		organization_id, skill_id, revision_id, archive_sha256, materialization_id, category, summary, correlation_id, source
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, organizationID, provenance.SkillID, provenance.RevisionID, provenance.ArchiveSHA256, provenance.MaterializationID, observation.Category, observation.Summary, observation.CorrelationID, observation.Source)
	if err != nil {
		return FeedbackRecord{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return FeedbackRecord{}, err
	}
	var record FeedbackRecord
	err = s.DB.QueryRowContext(ctx, `SELECT id, skill_id, revision_id, archive_sha256, materialization_id, category, summary, correlation_id, source, created_at FROM skill_feedback WHERE id=? AND organization_id=?`, id, organizationID).Scan(
		&record.ID, &record.SkillID, &record.RevisionID, &record.ArchiveSHA256, &record.MaterializationID, &record.Category, &record.Summary, &record.CorrelationID, &record.Source, &record.CreatedAt,
	)
	return record, err
}

func (s *Store) ListFeedback(ctx context.Context, organizationID, skillID, revisionID, category string, limit, offset int) ([]FeedbackRecord, error) {
	if s == nil || s.DB == nil || organizationID == "" {
		return nil, fmt.Errorf("catalogue database and organization are required")
	}
	if skillID == "" && revisionID == "" {
		return nil, fmt.Errorf("skill_id or revision_id is required")
	}
	if category != "" && !validFeedbackCategory(category) {
		return nil, fmt.Errorf("unsupported feedback category %q", category)
	}
	if limit < 1 || limit > 101 || offset < 0 {
		return nil, fmt.Errorf("feedback pagination is invalid")
	}

	query := `SELECT id, skill_id, revision_id, archive_sha256, materialization_id, category, summary, correlation_id, source, created_at FROM skill_feedback WHERE organization_id=?`
	args := []any{organizationID}
	if skillID != "" {
		query += ` AND skill_id=?`
		args = append(args, skillID)
	}
	if revisionID != "" {
		query += ` AND revision_id=?`
		args = append(args, revisionID)
	}
	if category != "" {
		query += ` AND category=?`
		args = append(args, category)
	}
	query += ` ORDER BY id DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []FeedbackRecord
	for rows.Next() {
		var record FeedbackRecord
		if err := rows.Scan(&record.ID, &record.SkillID, &record.RevisionID, &record.ArchiveSHA256, &record.MaterializationID, &record.Category, &record.Summary, &record.CorrelationID, &record.Source, &record.CreatedAt); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func validFeedbackCategory(category string) bool {
	switch strings.TrimSpace(category) {
	case "step_failed", "workaround_required", "user_correction", "ambiguous_instruction", "compatibility_mismatch", "improvement_suggested":
		return true
	default:
		return false
	}
}
