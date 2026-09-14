package curriculum

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

func (s *Store) CreateEvalSuiteVersion(ctx context.Context, input CreateEvalSuiteVersionInput) (EvalSuiteVersion, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.Name = strings.TrimSpace(input.Name)
	input.Version = strings.TrimSpace(input.Version)
	input.ParentVersion = strings.TrimSpace(input.ParentVersion)
	if input.OrganizationID == "" || input.ActorID == "" || input.Name == "" || input.Version == "" {
		return EvalSuiteVersion{}, fmt.Errorf("organization, actor, suite name, and version are required")
	}
	if input.ParentVersion == input.Version && input.ParentVersion != "" {
		return EvalSuiteVersion{}, fmt.Errorf("suite version cannot parent itself")
	}
	development, err := normalizeIDs(input.DevelopmentProposalIDs)
	if err != nil {
		return EvalSuiteVersion{}, fmt.Errorf("development proposals: %w", err)
	}
	heldOut, err := normalizeIDs(input.HeldOutProposalIDs)
	if err != nil {
		return EvalSuiteVersion{}, fmt.Errorf("held-out proposals: %w", err)
	}
	if len(development)+len(heldOut) == 0 {
		return EvalSuiteVersion{}, fmt.Errorf("eval suite version requires at least one reviewed eval proposal")
	}
	if overlaps(development, heldOut) {
		return EvalSuiteVersion{}, fmt.Errorf("a proposal cannot be both development and held-out")
	}
	for _, id := range development {
		if err := s.requireAcceptedProposal(ctx, input.OrganizationID, id, ProposalDevelopmentEval, AudienceDevelopment); err != nil {
			return EvalSuiteVersion{}, fmt.Errorf("development proposal %s: %w", id, err)
		}
	}
	for _, id := range heldOut {
		if err := s.requireAcceptedProposal(ctx, input.OrganizationID, id, ProposalProtectedEval, AudienceHeldOut); err != nil {
			return EvalSuiteVersion{}, fmt.Errorf("held-out proposal %s: %w", id, err)
		}
	}
	if input.ParentVersion != "" {
		parent, err := s.GetEvalSuiteVersionByName(ctx, input.OrganizationID, input.Name, input.ParentVersion)
		if err != nil {
			return EvalSuiteVersion{}, fmt.Errorf("parent suite version: %w", err)
		}
		if !containsAll(development, parent.DevelopmentProposalIDs) || !containsAll(heldOut, parent.HeldOutProposalIDs) {
			return EvalSuiteVersion{}, fmt.Errorf("new suite versions cannot silently remove reviewed parent cases; represent changes as additional reviewed proposal versions")
		}
	}
	protected := struct {
		Name                   string   `json:"name"`
		Version                string   `json:"version"`
		ParentVersion          string   `json:"parent_version,omitempty"`
		DevelopmentProposalIDs []string `json:"development_proposal_ids"`
		HeldOutProposalIDs     []string `json:"held_out_proposal_ids"`
	}{input.Name, input.Version, input.ParentVersion, development, heldOut}
	raw, err := json.Marshal(protected)
	if err != nil {
		return EvalSuiteVersion{}, err
	}
	revision := revisionDigest(raw)
	id := contentID("cursuite_", input.OrganizationID, raw)
	if existing, err := s.GetEvalSuiteVersionByName(ctx, input.OrganizationID, input.Name, input.Version); err == nil {
		if existing.SuiteRevision != revision {
			return EvalSuiteVersion{}, fmt.Errorf("eval suite name/version already exists with different immutable inputs; create a new suite version")
		}
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return EvalSuiteVersion{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO curriculum_eval_suite_versions(id, organization_id, name, version, payload_json, suite_revision, actor_id, correlation_id) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, input.Name, input.Version, string(raw), revision, input.ActorID, input.CorrelationID)
	if err != nil {
		return EvalSuiteVersion{}, err
	}
	return s.GetEvalSuiteVersion(ctx, input.OrganizationID, id)
}

func (s *Store) GetEvalSuiteVersion(ctx context.Context, organizationID, id string) (EvalSuiteVersion, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return EvalSuiteVersion{}, fmt.Errorf("organization and eval suite version id are required")
	}
	var raw, revision, actorID, correlationID, createdAt string
	if err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT payload_json, suite_revision, actor_id, correlation_id, created_at FROM curriculum_eval_suite_versions WHERE organization_id=? AND id=?`, organizationID, id).Scan(&raw, &revision, &actorID, &correlationID, &createdAt); err != nil {
		return EvalSuiteVersion{}, err
	}
	var payload struct {
		Name                   string   `json:"name"`
		Version                string   `json:"version"`
		ParentVersion          string   `json:"parent_version,omitempty"`
		DevelopmentProposalIDs []string `json:"development_proposal_ids"`
		HeldOutProposalIDs     []string `json:"held_out_proposal_ids"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return EvalSuiteVersion{}, fmt.Errorf("decode eval suite version: %w", err)
	}
	return EvalSuiteVersion{ID: id, OrganizationID: organizationID, Name: payload.Name, Version: payload.Version, ParentVersion: payload.ParentVersion, DevelopmentProposalIDs: payload.DevelopmentProposalIDs, HeldOutProposalIDs: payload.HeldOutProposalIDs, SuiteRevision: revision, ActorID: actorID, CorrelationID: correlationID, CreatedAt: createdAt}, nil
}

func (s *Store) GetEvalSuiteVersionByName(ctx context.Context, organizationID, name, version string) (EvalSuiteVersion, error) {
	var id string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id FROM curriculum_eval_suite_versions WHERE organization_id=? AND name=? AND version=?`, strings.TrimSpace(organizationID), strings.TrimSpace(name), strings.TrimSpace(version)).Scan(&id)
	if err != nil {
		return EvalSuiteVersion{}, err
	}
	return s.GetEvalSuiteVersion(ctx, organizationID, id)
}

func (s *Store) PrepareCandidateHandoff(ctx context.Context, input PrepareHandoffInput) (CandidateHandoff, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.GapID = strings.TrimSpace(input.GapID)
	if input.OrganizationID == "" || input.GapID == "" {
		return CandidateHandoff{}, fmt.Errorf("organization and gap id are required")
	}
	gap, err := s.GetGap(ctx, input.OrganizationID, input.GapID)
	if err != nil {
		return CandidateHandoff{}, err
	}
	ids, err := normalizeIDs(input.ProposalIDs)
	if err != nil {
		return CandidateHandoff{}, err
	}
	if len(ids) == 0 {
		return CandidateHandoff{}, fmt.Errorf("at least one proposal id is required")
	}
	included := make([]HandoffProposal, 0, len(ids))
	excluded := 0
	for _, id := range ids {
		proposal, err := s.GetProposal(ctx, input.OrganizationID, id)
		if err != nil {
			return CandidateHandoff{}, err
		}
		if proposal.GapID != gap.ID {
			return CandidateHandoff{}, fmt.Errorf("proposal %s belongs to a different capability gap", id)
		}
		review, err := s.GetReview(ctx, input.OrganizationID, id)
		if err != nil {
			return CandidateHandoff{}, fmt.Errorf("proposal %s is not explicitly reviewed: %w", id, err)
		}
		if review.Decision != ReviewAccepted {
			return CandidateHandoff{}, fmt.Errorf("proposal %s is not accepted", id)
		}
		if proposal.Audience == AudienceHeldOut || proposal.Kind == ProposalProtectedEval {
			excluded++
			continue
		}
		included = append(included, HandoffProposal{ID: proposal.ID, Name: proposal.Name, Version: proposal.Version, Kind: proposal.Kind, Title: proposal.Title, Intent: proposal.Intent, ArtifactReference: proposal.ArtifactReference, ArtifactSHA256: proposal.ArtifactSHA256, Oracle: proposal.Oracle})
	}
	sort.Slice(included, func(i, j int) bool { return included[i].ID < included[j].ID })
	protected := struct {
		GapID     string            `json:"gap_id"`
		Scope     Scope             `json:"scope"`
		Proposals []HandoffProposal `json:"proposals"`
	}{gap.ID, gap.Scope, included}
	raw, err := json.Marshal(protected)
	if err != nil {
		return CandidateHandoff{}, err
	}
	return CandidateHandoff{SchemaVersion: "1", ID: contentID("curhandoff_", input.OrganizationID, raw), OrganizationID: input.OrganizationID, GapID: gap.ID, Scope: gap.Scope, Proposals: included, ExcludedHeldOutCount: excluded}, nil
}

func (s *Store) requireAcceptedProposal(ctx context.Context, organizationID, id, kind, audience string) error {
	proposal, err := s.GetProposal(ctx, organizationID, id)
	if err != nil {
		return err
	}
	if proposal.Kind != kind || proposal.Audience != audience {
		return fmt.Errorf("expected %s/%s proposal, got %s/%s", kind, audience, proposal.Kind, proposal.Audience)
	}
	review, err := s.GetReview(ctx, organizationID, id)
	if err != nil {
		return fmt.Errorf("explicit review is required: %w", err)
	}
	if review.Decision != ReviewAccepted {
		return fmt.Errorf("proposal review decision is %s", review.Decision)
	}
	return nil
}
