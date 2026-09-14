package curriculum

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mhingston/skillet/internal/catalogue"
)

type Store struct {
	Catalogue *catalogue.Store
}

func New(ctx context.Context, catalog *catalogue.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil {
		return nil, fmt.Errorf("curriculum catalogue is required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS curriculum_gaps (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			payload_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_curriculum_gaps_scope ON curriculum_gaps(organization_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS curriculum_proposals (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			gap_id TEXT NOT NULL REFERENCES curriculum_gaps(id),
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			proposal_revision TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, name, version)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_curriculum_proposals_gap ON curriculum_proposals(organization_id, gap_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS curriculum_reviews (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			proposal_id TEXT NOT NULL REFERENCES curriculum_proposals(id),
			decision TEXT NOT NULL CHECK(decision IN ('accepted','rejected')),
			reference TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, proposal_id)
		)`,
		`CREATE TABLE IF NOT EXISTS curriculum_eval_suite_versions (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			name TEXT NOT NULL,
			version TEXT NOT NULL,
			payload_json TEXT NOT NULL,
			suite_revision TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, name, version)
		)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize curriculum schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog}, nil
}

func (s *Store) RecordGap(ctx context.Context, input RecordGapInput) (CapabilityGap, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.FailureKey = strings.TrimSpace(input.FailureKey)
	if input.OrganizationID == "" || input.ActorID == "" || input.FailureKey == "" {
		return CapabilityGap{}, fmt.Errorf("organization, actor, and failure_key are required")
	}
	if err := validateText("actor", input.ActorID, MaxIdentityBytes); err != nil {
		return CapabilityGap{}, err
	}
	if err := validateText("failure_key", input.FailureKey, MaxIdentityBytes); err != nil {
		return CapabilityGap{}, err
	}
	scope, err := s.normalizeScope(ctx, input.OrganizationID, input.Scope)
	if err != nil {
		return CapabilityGap{}, err
	}
	evidence, err := normalizeEvidence(input.Evidence)
	if err != nil {
		return CapabilityGap{}, err
	}
	if len(evidence) == 0 {
		return CapabilityGap{}, fmt.Errorf("at least one evidence reference is required")
	}
	if len(evidence) < 2 && requiresRepeatedEvidence(evidence[0].Kind) {
		return CapabilityGap{}, fmt.Errorf("%s requires at least two observations before it becomes a capability gap", evidence[0].Kind)
	}
	payload := struct {
		Scope           Scope               `json:"scope"`
		FailureKey      string              `json:"failure_key"`
		Evidence        []EvidenceReference `json:"evidence"`
		OccurrenceCount int                 `json:"occurrence_count"`
	}{scope, input.FailureKey, evidence, len(evidence)}
	raw, err := json.Marshal(payload)
	if err != nil {
		return CapabilityGap{}, err
	}
	id := contentID("gap_", input.OrganizationID, raw)
	if existing, err := s.GetGap(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CapabilityGap{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO curriculum_gaps(id, organization_id, payload_json, actor_id, correlation_id) VALUES (?, ?, ?, ?, ?)`, id, input.OrganizationID, string(raw), input.ActorID, input.CorrelationID)
	if err != nil {
		return CapabilityGap{}, err
	}
	return s.GetGap(ctx, input.OrganizationID, id)
}

func (s *Store) GetGap(ctx context.Context, organizationID, id string) (CapabilityGap, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return CapabilityGap{}, fmt.Errorf("organization and gap id are required")
	}
	var raw, actorID, correlationID, createdAt string
	if err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT payload_json, actor_id, correlation_id, created_at FROM curriculum_gaps WHERE organization_id=? AND id=?`, organizationID, id).Scan(&raw, &actorID, &correlationID, &createdAt); err != nil {
		return CapabilityGap{}, err
	}
	var payload struct {
		Scope           Scope               `json:"scope"`
		FailureKey      string              `json:"failure_key"`
		Evidence        []EvidenceReference `json:"evidence"`
		OccurrenceCount int                 `json:"occurrence_count"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return CapabilityGap{}, fmt.Errorf("decode capability gap: %w", err)
	}
	return CapabilityGap{ID: id, OrganizationID: organizationID, Scope: payload.Scope, FailureKey: payload.FailureKey, Evidence: payload.Evidence, OccurrenceCount: payload.OccurrenceCount, ActorID: actorID, CorrelationID: correlationID, CreatedAt: createdAt}, nil
}

func (s *Store) CreateProposal(ctx context.Context, input CreateProposalInput) (Proposal, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.GapID = strings.TrimSpace(input.GapID)
	input.Name = strings.TrimSpace(input.Name)
	input.Version = strings.TrimSpace(input.Version)
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	input.Intent = strings.TrimSpace(input.Intent)
	input.ArtifactReference = strings.TrimSpace(input.ArtifactReference)
	input.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(input.ArtifactSHA256))
	if input.OrganizationID == "" || input.ActorID == "" || input.GapID == "" || input.Name == "" || input.Version == "" || input.Kind == "" || input.Title == "" || input.ArtifactReference == "" {
		return Proposal{}, fmt.Errorf("organization, actor, gap, name, version, kind, title, and artifact reference are required")
	}
	if _, err := s.GetGap(ctx, input.OrganizationID, input.GapID); err != nil {
		return Proposal{}, err
	}
	for field, value := range map[string]string{"name": input.Name, "version": input.Version, "title": input.Title} {
		if err := validateText(field, value, MaxIdentityBytes); err != nil {
			return Proposal{}, err
		}
	}
	if err := validateText("intent", input.Intent, MaxSummaryBytes); err != nil {
		return Proposal{}, err
	}
	if err := validateText("artifact_reference", input.ArtifactReference, MaxReferenceBytes); err != nil {
		return Proposal{}, err
	}
	if !digestPattern.MatchString(input.ArtifactSHA256) {
		return Proposal{}, fmt.Errorf("artifact_sha256 must be a lowercase 64-character SHA-256 digest")
	}
	audience, err := audienceForKind(input.Kind)
	if err != nil {
		return Proposal{}, err
	}
	oracle, err := normalizeOracle(input.Oracle)
	if err != nil {
		return Proposal{}, err
	}
	protected := struct {
		GapID             string `json:"gap_id"`
		Name              string `json:"name"`
		Version           string `json:"version"`
		Kind              string `json:"kind"`
		Audience          string `json:"audience"`
		Title             string `json:"title"`
		Intent            string `json:"intent"`
		ArtifactReference string `json:"artifact_reference"`
		ArtifactSHA256    string `json:"artifact_sha256"`
		Oracle            Oracle `json:"oracle"`
	}{input.GapID, input.Name, input.Version, input.Kind, audience, input.Title, input.Intent, input.ArtifactReference, input.ArtifactSHA256, oracle}
	raw, err := json.Marshal(protected)
	if err != nil {
		return Proposal{}, err
	}
	revision := revisionDigest(raw)
	id := contentID("curprop_", input.OrganizationID, raw)
	if existing, err := s.getProposalByNameVersion(ctx, input.OrganizationID, input.Name, input.Version); err == nil {
		if existing.ProposalRevision != revision {
			return Proposal{}, fmt.Errorf("curriculum proposal name/version already exists with different immutable content")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Proposal{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO curriculum_proposals(id, organization_id, gap_id, name, version, payload_json, proposal_revision, actor_id, correlation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, input.GapID, input.Name, input.Version, string(raw), revision, input.ActorID, input.CorrelationID)
	if err != nil {
		return Proposal{}, err
	}
	return s.GetProposal(ctx, input.OrganizationID, id)
}

func (s *Store) GetProposal(ctx context.Context, organizationID, id string) (Proposal, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return Proposal{}, fmt.Errorf("organization and proposal id are required")
	}
	var gapID, name, version, raw, revision, actorID, correlationID, createdAt string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT gap_id, name, version, payload_json, proposal_revision, actor_id, correlation_id, created_at FROM curriculum_proposals WHERE organization_id=? AND id=?`, organizationID, id).Scan(&gapID, &name, &version, &raw, &revision, &actorID, &correlationID, &createdAt)
	if err != nil {
		return Proposal{}, err
	}
	var payload struct {
		Kind              string `json:"kind"`
		Audience          string `json:"audience"`
		Title             string `json:"title"`
		Intent            string `json:"intent"`
		ArtifactReference string `json:"artifact_reference"`
		ArtifactSHA256    string `json:"artifact_sha256"`
		Oracle            Oracle `json:"oracle"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Proposal{}, fmt.Errorf("decode curriculum proposal: %w", err)
	}
	return Proposal{ID: id, OrganizationID: organizationID, GapID: gapID, Name: name, Version: version, Kind: payload.Kind, Audience: payload.Audience, Title: payload.Title, Intent: payload.Intent, ArtifactReference: payload.ArtifactReference, ArtifactSHA256: payload.ArtifactSHA256, Oracle: payload.Oracle, ProposalRevision: revision, ActorID: actorID, CorrelationID: correlationID, CreatedAt: createdAt}, nil
}

func (s *Store) getProposalByNameVersion(ctx context.Context, organizationID, name, version string) (Proposal, error) {
	var id string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id FROM curriculum_proposals WHERE organization_id=? AND name=? AND version=?`, organizationID, name, version).Scan(&id)
	if err != nil {
		return Proposal{}, err
	}
	return s.GetProposal(ctx, organizationID, id)
}

func (s *Store) ReviewProposal(ctx context.Context, input ReviewInput) (Review, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ProposalID = strings.TrimSpace(input.ProposalID)
	input.Decision = strings.TrimSpace(input.Decision)
	input.Reference = strings.TrimSpace(input.Reference)
	if input.OrganizationID == "" || input.ActorID == "" || input.ProposalID == "" || input.Reference == "" {
		return Review{}, fmt.Errorf("organization, actor, proposal, and review reference are required")
	}
	if input.Decision != ReviewAccepted && input.Decision != ReviewRejected {
		return Review{}, fmt.Errorf("review decision must be %q or %q", ReviewAccepted, ReviewRejected)
	}
	if err := validateText("review reference", input.Reference, MaxReferenceBytes); err != nil {
		return Review{}, err
	}
	if _, err := s.GetProposal(ctx, input.OrganizationID, input.ProposalID); err != nil {
		return Review{}, err
	}
	if existing, err := s.GetReview(ctx, input.OrganizationID, input.ProposalID); err == nil {
		if existing.Decision == input.Decision && existing.Reference == input.Reference {
			return existing, nil
		}
		return Review{}, fmt.Errorf("proposal review is immutable; create a new proposal version for a different decision")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Review{}, err
	}
	payload, _ := json.Marshal([]string{input.ProposalID, input.Decision, input.Reference, input.ActorID})
	id := contentID("currev_", input.OrganizationID, payload)
	_, err := s.Catalogue.DB.ExecContext(ctx, `INSERT INTO curriculum_reviews(id, organization_id, proposal_id, decision, reference, actor_id, correlation_id) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, input.ProposalID, input.Decision, input.Reference, input.ActorID, input.CorrelationID)
	if err != nil {
		return Review{}, err
	}
	return s.GetReview(ctx, input.OrganizationID, input.ProposalID)
}

func (s *Store) GetReview(ctx context.Context, organizationID, proposalID string) (Review, error) {
	organizationID, proposalID = strings.TrimSpace(organizationID), strings.TrimSpace(proposalID)
	if organizationID == "" || proposalID == "" {
		return Review{}, fmt.Errorf("organization and proposal id are required")
	}
	var item Review
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, proposal_id, decision, reference, actor_id, correlation_id, created_at FROM curriculum_reviews WHERE organization_id=? AND proposal_id=?`, organizationID, proposalID).Scan(&item.ID, &item.OrganizationID, &item.ProposalID, &item.Decision, &item.Reference, &item.ActorID, &item.CorrelationID, &item.CreatedAt)
	return item, err
}
