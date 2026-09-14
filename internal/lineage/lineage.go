// Package lineage owns the opt-in, append-only evolutionary history for
// capability revisions and experiment-produced descendants. Lineage is evidence:
// it never participates in package dependency, version resolution, activation,
// trust, governance, or retrieval ranking.
package lineage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mhingston/skillet/internal/catalogue"
)

const (
	DescendantCandidate = "candidate"
	DescendantRevision  = "revision"

	RelationshipDerivedFrom  = "derived_from"
	RelationshipChallengerOf  = "challenger_of"
	RelationshipSupersededBy  = "superseded_by"

	DecisionPromoted = "promoted"
	DecisionRejected = "rejected"

	MaxExperiments      = 64
	MaxReferenceBytes   = 2 * 1024
	MaxIdentityBytes    = 256
	DefaultViewLimit    = 25
	MaximumViewLimit    = 100
)

var ErrCycle = errors.New("lineage write would create a cycle")

type Record struct {
	ID               string   `json:"id"`
	OrganizationID   string   `json:"organization_id"`
	CapabilityID     string   `json:"capability_id"`
	ParentRevisionID string   `json:"parent_revision_id"`
	DescendantKind   string   `json:"descendant_kind"`
	DescendantID     string   `json:"descendant_id"`
	Relationship     string   `json:"relationship"`
	ExperimentIDs    []string `json:"experiment_ids"`
	ActorID          string   `json:"actor_id"`
	CorrelationID    string   `json:"correlation_id,omitempty"`
	CreatedAt        string   `json:"created_at"`
}

type Decision struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	LineageID      string `json:"lineage_id"`
	State          string `json:"state"`
	Reference      string `json:"reference"`
	ActorID        string `json:"actor_id"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type ExperimentEvidence struct {
	ID              string `json:"id"`
	BaseRevisionID  string `json:"base_revision_id"`
	CandidateID     string `json:"candidate_id"`
	Status          string `json:"status"`
	SpecRevision    string `json:"spec_revision"`
	ResultSHA256    string `json:"result_sha256,omitempty"`
	EvalSuiteID     string `json:"eval_suite_id,omitempty"`
	EvalSuiteVersion string `json:"eval_suite_version,omitempty"`
}

type Entry struct {
	Record      Record               `json:"record"`
	Experiments []ExperimentEvidence `json:"experiments"`
	Decision    *Decision            `json:"decision,omitempty"`
}

type View struct {
	CapabilityID string  `json:"capability_id"`
	RevisionID   string  `json:"revision_id"`
	Parents      []Entry `json:"parents"`
	Descendants  []Entry `json:"descendants"`
}

type RecordInput struct {
	OrganizationID   string
	ActorID           string
	CorrelationID     string
	ParentRevisionID  string
	DescendantKind    string
	DescendantID      string
	Relationship      string
	ExperimentIDs     []string
}

type DecisionInput struct {
	OrganizationID string
	ActorID        string
	CorrelationID  string
	LineageID      string
	State          string
	Reference      string
}

type Store struct {
	Catalogue *catalogue.Store
}

func New(ctx context.Context, catalog *catalogue.Store) (*Store, error) {
	if catalog == nil || catalog.DB == nil {
		return nil, fmt.Errorf("lineage catalogue is required")
	}
	statements := []string{
		`CREATE TABLE IF NOT EXISTS improvement_lineage (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			capability_id TEXT NOT NULL,
			parent_revision_id TEXT NOT NULL,
			descendant_kind TEXT NOT NULL CHECK(descendant_kind IN ('candidate','revision')),
			descendant_id TEXT NOT NULL,
			relationship TEXT NOT NULL CHECK(relationship IN ('derived_from','challenger_of','superseded_by')),
			experiment_ids_json TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_improvement_lineage_parent ON improvement_lineage(organization_id, capability_id, parent_revision_id, created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_improvement_lineage_descendant ON improvement_lineage(organization_id, capability_id, descendant_kind, descendant_id, created_at, id)`,
		`CREATE TABLE IF NOT EXISTS improvement_lineage_decisions (
			id TEXT PRIMARY KEY,
			organization_id TEXT NOT NULL REFERENCES organizations(id),
			lineage_id TEXT NOT NULL REFERENCES improvement_lineage(id),
			state TEXT NOT NULL CHECK(state IN ('promoted','rejected')),
			reference TEXT NOT NULL,
			actor_id TEXT NOT NULL,
			correlation_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(organization_id, lineage_id)
		)`,
	}
	for _, statement := range statements {
		if _, err := catalog.DB.ExecContext(ctx, statement); err != nil {
			return nil, fmt.Errorf("initialize lineage schema: %w", err)
		}
	}
	return &Store{Catalogue: catalog}, nil
}

func (s *Store) Record(ctx context.Context, input RecordInput) (Entry, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.ParentRevisionID = strings.TrimSpace(input.ParentRevisionID)
	input.DescendantKind = strings.TrimSpace(input.DescendantKind)
	input.DescendantID = strings.TrimSpace(input.DescendantID)
	input.Relationship = strings.TrimSpace(input.Relationship)
	if input.OrganizationID == "" || input.ActorID == "" || input.ParentRevisionID == "" || input.DescendantID == "" {
		return Entry{}, fmt.Errorf("organization, actor, parent revision, and descendant are required")
	}
	if err := validateIdentity("actor", input.ActorID); err != nil {
		return Entry{}, err
	}
	if input.CorrelationID != "" {
		if err := validateIdentity("correlation id", input.CorrelationID); err != nil {
			return Entry{}, err
		}
	}
	if input.DescendantKind != DescendantCandidate && input.DescendantKind != DescendantRevision {
		return Entry{}, fmt.Errorf("descendant kind must be candidate or revision")
	}
	switch input.Relationship {
	case RelationshipDerivedFrom, RelationshipChallengerOf, RelationshipSupersededBy:
	default:
		return Entry{}, fmt.Errorf("unsupported lineage relationship")
	}

	parent, err := s.Catalogue.Revision(ctx, input.OrganizationID, input.ParentRevisionID)
	if err != nil {
		return Entry{}, err
	}
	capabilityID := parent.SkillID
	if capabilityID == "" {
		return Entry{}, fmt.Errorf("parent revision has no capability identity")
	}
	if input.DescendantKind == DescendantRevision {
		if input.DescendantID == input.ParentRevisionID {
			return Entry{}, ErrCycle
		}
		descendant, err := s.Catalogue.Revision(ctx, input.OrganizationID, input.DescendantID)
		if err != nil {
			return Entry{}, err
		}
		if descendant.SkillID != capabilityID {
			return Entry{}, fmt.Errorf("descendant revision belongs to a different capability")
		}
		cyclic, err := s.wouldCycle(ctx, input.OrganizationID, capabilityID, input.ParentRevisionID, input.DescendantID)
		if err != nil {
			return Entry{}, err
		}
		if cyclic {
			return Entry{}, ErrCycle
		}
	}

	experimentIDs, err := normalizeExperimentIDs(input.ExperimentIDs)
	if err != nil {
		return Entry{}, err
	}
	experiments, err := s.loadAndValidateExperiments(ctx, input.OrganizationID, capabilityID, input.ParentRevisionID, input.DescendantKind, input.DescendantID, experimentIDs)
	if err != nil {
		return Entry{}, err
	}
	experimentJSON, err := json.Marshal(experimentIDs)
	if err != nil {
		return Entry{}, err
	}
	id := lineageID(input.OrganizationID, capabilityID, input.ParentRevisionID, input.DescendantKind, input.DescendantID, input.Relationship, experimentIDs)
	if existing, err := s.Get(ctx, input.OrganizationID, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Entry{}, err
	}
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improvement_lineage(
		id, organization_id, capability_id, parent_revision_id, descendant_kind,
		descendant_id, relationship, experiment_ids_json, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, input.OrganizationID, capabilityID, input.ParentRevisionID, input.DescendantKind,
		input.DescendantID, input.Relationship, string(experimentJSON), input.ActorID, input.CorrelationID)
	if err != nil {
		return Entry{}, err
	}
	entry, err := s.Get(ctx, input.OrganizationID, id)
	if err != nil {
		return Entry{}, err
	}
	entry.Experiments = experiments
	return entry, nil
}

func (s *Store) Decide(ctx context.Context, input DecisionInput) (Entry, error) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.CorrelationID = strings.TrimSpace(input.CorrelationID)
	input.LineageID = strings.TrimSpace(input.LineageID)
	input.State = strings.TrimSpace(input.State)
	input.Reference = strings.TrimSpace(input.Reference)
	if input.OrganizationID == "" || input.ActorID == "" || input.LineageID == "" || input.Reference == "" {
		return Entry{}, fmt.Errorf("organization, actor, lineage id, and decision reference are required")
	}
	if input.State != DecisionPromoted && input.State != DecisionRejected {
		return Entry{}, fmt.Errorf("decision state must be promoted or rejected")
	}
	if err := validateIdentity("actor", input.ActorID); err != nil {
		return Entry{}, err
	}
	if input.CorrelationID != "" {
		if err := validateIdentity("correlation id", input.CorrelationID); err != nil {
			return Entry{}, err
		}
	}
	if err := validateReference(input.Reference); err != nil {
		return Entry{}, err
	}
	entry, err := s.Get(ctx, input.OrganizationID, input.LineageID)
	if err != nil {
		return Entry{}, err
	}
	if entry.Decision != nil {
		if entry.Decision.State == input.State && entry.Decision.Reference == input.Reference {
			return entry, nil
		}
		return Entry{}, fmt.Errorf("lineage already has an immutable terminal decision")
	}
	id := decisionID(input.OrganizationID, input.LineageID, input.State, input.Reference)
	_, err = s.Catalogue.DB.ExecContext(ctx, `INSERT INTO improvement_lineage_decisions(
		id, organization_id, lineage_id, state, reference, actor_id, correlation_id
	) VALUES (?, ?, ?, ?, ?, ?, ?)`, id, input.OrganizationID, input.LineageID, input.State, input.Reference, input.ActorID, input.CorrelationID)
	if err != nil {
		return Entry{}, err
	}
	return s.Get(ctx, input.OrganizationID, input.LineageID)
}

func (s *Store) Get(ctx context.Context, organizationID, id string) (Entry, error) {
	organizationID, id = strings.TrimSpace(organizationID), strings.TrimSpace(id)
	if organizationID == "" || id == "" {
		return Entry{}, fmt.Errorf("organization and lineage id are required")
	}
	var record Record
	var experimentsJSON string
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, capability_id, parent_revision_id,
		descendant_kind, descendant_id, relationship, experiment_ids_json, actor_id, correlation_id, created_at
		FROM improvement_lineage WHERE organization_id=? AND id=?`, organizationID, id).Scan(
		&record.ID, &record.OrganizationID, &record.CapabilityID, &record.ParentRevisionID,
		&record.DescendantKind, &record.DescendantID, &record.Relationship, &experimentsJSON,
		&record.ActorID, &record.CorrelationID, &record.CreatedAt)
	if err != nil {
		return Entry{}, err
	}
	if err := json.Unmarshal([]byte(experimentsJSON), &record.ExperimentIDs); err != nil {
		return Entry{}, fmt.Errorf("decode lineage experiment ids: %w", err)
	}
	experiments, err := s.loadExperiments(ctx, organizationID, record.ExperimentIDs)
	if err != nil {
		return Entry{}, err
	}
	decision, err := s.loadDecision(ctx, organizationID, record.ID)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Record: record, Experiments: experiments, Decision: decision}, nil
}

func (s *Store) View(ctx context.Context, organizationID, revisionID string, limit int) (View, error) {
	organizationID, revisionID = strings.TrimSpace(organizationID), strings.TrimSpace(revisionID)
	if limit == 0 {
		limit = DefaultViewLimit
	}
	if limit < 1 || limit > MaximumViewLimit {
		return View{}, fmt.Errorf("lineage view limit must be between 1 and %d", MaximumViewLimit)
	}
	info, err := s.Catalogue.Revision(ctx, organizationID, revisionID)
	if err != nil {
		return View{}, err
	}
	view := View{CapabilityID: info.SkillID, RevisionID: revisionID, Parents: []Entry{}, Descendants: []Entry{}}
	parentIDs, err := s.queryIDs(ctx, `SELECT id FROM improvement_lineage WHERE organization_id=? AND capability_id=? AND descendant_kind='revision' AND descendant_id=? ORDER BY created_at DESC, id LIMIT ?`, organizationID, info.SkillID, revisionID, limit)
	if err != nil {
		return View{}, err
	}
	descendantIDs, err := s.queryIDs(ctx, `SELECT id FROM improvement_lineage WHERE organization_id=? AND capability_id=? AND parent_revision_id=? ORDER BY created_at DESC, id LIMIT ?`, organizationID, info.SkillID, revisionID, limit)
	if err != nil {
		return View{}, err
	}
	for _, id := range parentIDs {
		entry, err := s.Get(ctx, organizationID, id)
		if err != nil {
			return View{}, err
		}
		view.Parents = append(view.Parents, entry)
	}
	for _, id := range descendantIDs {
		entry, err := s.Get(ctx, organizationID, id)
		if err != nil {
			return View{}, err
		}
		view.Descendants = append(view.Descendants, entry)
	}
	return view, nil
}

func (s *Store) queryIDs(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.Catalogue.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) wouldCycle(ctx context.Context, organizationID, capabilityID, parentRevisionID, descendantRevisionID string) (bool, error) {
	var found int
	err := s.Catalogue.DB.QueryRowContext(ctx, `WITH RECURSIVE reachable(revision_id) AS (
		SELECT descendant_id FROM improvement_lineage
		WHERE organization_id=? AND capability_id=? AND parent_revision_id=? AND descendant_kind='revision'
		UNION
		SELECT l.descendant_id FROM improvement_lineage l
		JOIN reachable r ON l.parent_revision_id=r.revision_id
		WHERE l.organization_id=? AND l.capability_id=? AND l.descendant_kind='revision'
	)
	SELECT 1 FROM reachable WHERE revision_id=? LIMIT 1`,
		organizationID, capabilityID, descendantRevisionID,
		organizationID, capabilityID, parentRevisionID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil && found == 1, err
}

func (s *Store) loadAndValidateExperiments(ctx context.Context, organizationID, capabilityID, parentRevisionID, descendantKind, descendantID string, ids []string) ([]ExperimentEvidence, error) {
	experiments, err := s.loadExperiments(ctx, organizationID, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range experiments {
		if item.BaseRevisionID != parentRevisionID {
			return nil, fmt.Errorf("experiment %q is bound to a different base revision", item.ID)
		}
		var experimentCapability string
		if err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT capability_id FROM improvement_experiments WHERE organization_id=? AND id=?`, organizationID, item.ID).Scan(&experimentCapability); err != nil {
			return nil, err
		}
		if experimentCapability != capabilityID {
			return nil, fmt.Errorf("experiment %q belongs to a different capability", item.ID)
		}
		if descendantKind == DescendantCandidate && item.CandidateID != descendantID {
			return nil, fmt.Errorf("experiment %q does not produce descendant candidate %q", item.ID, descendantID)
		}
	}
	return experiments, nil
}

func (s *Store) loadExperiments(ctx context.Context, organizationID string, ids []string) ([]ExperimentEvidence, error) {
	out := make([]ExperimentEvidence, 0, len(ids))
	for _, id := range ids {
		var item ExperimentEvidence
		var evalJSON string
		err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, base_revision_id, candidate_id, status, spec_revision, result_sha256, eval_suite_json
			FROM improvement_experiments WHERE organization_id=? AND id=?`, organizationID, id).Scan(
			&item.ID, &item.BaseRevisionID, &item.CandidateID, &item.Status, &item.SpecRevision, &item.ResultSHA256, &evalJSON)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("experiment %q not found in organization scope", id)
		}
		if err != nil {
			return nil, err
		}
		var suite struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(evalJSON), &suite); err != nil {
			return nil, fmt.Errorf("decode experiment %q eval suite: %w", id, err)
		}
		item.EvalSuiteID = suite.ID
		item.EvalSuiteVersion = suite.Version
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) loadDecision(ctx context.Context, organizationID, lineageID string) (*Decision, error) {
	var decision Decision
	err := s.Catalogue.DB.QueryRowContext(ctx, `SELECT id, organization_id, lineage_id, state, reference, actor_id, correlation_id, created_at
		FROM improvement_lineage_decisions WHERE organization_id=? AND lineage_id=?`, organizationID, lineageID).Scan(
		&decision.ID, &decision.OrganizationID, &decision.LineageID, &decision.State, &decision.Reference,
		&decision.ActorID, &decision.CorrelationID, &decision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &decision, nil
}

func normalizeExperimentIDs(ids []string) ([]string, error) {
	if len(ids) == 0 || len(ids) > MaxExperiments {
		return nil, fmt.Errorf("lineage requires between 1 and %d producing experiments", MaxExperiments)
	}
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("experiment id is required")
		}
		if err := validateIdentity("experiment id", id); err != nil {
			return nil, err
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one unique experiment id is required")
	}
	sort.Strings(out)
	return out, nil
}

func validateIdentity(name, value string) error {
	if !utf8.ValidString(value) || len([]byte(value)) > MaxIdentityBytes {
		return fmt.Errorf("%s exceeds bounds or is not valid UTF-8", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", name)
		}
	}
	return nil
}

func validateReference(value string) error {
	if !utf8.ValidString(value) || len([]byte(value)) > MaxReferenceBytes {
		return fmt.Errorf("decision reference exceeds bounds or is not valid UTF-8")
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("decision reference contains control characters")
		}
	}
	return nil
}

func lineageID(organizationID, capabilityID, parentRevisionID, descendantKind, descendantID, relationship string, experimentIDs []string) string {
	value := strings.Join([]string{organizationID, capabilityID, parentRevisionID, descendantKind, descendantID, relationship, strings.Join(experimentIDs, "\x1f")}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return "lin_" + hex.EncodeToString(sum[:16])
}

func decisionID(organizationID, lineageID, state, reference string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{organizationID, lineageID, state, reference}, "\x00")))
	return "lindec_" + hex.EncodeToString(sum[:16])
}
