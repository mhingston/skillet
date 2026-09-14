// Package curriculum owns opt-in, append-only curriculum and capability-gap
// evidence. It can describe what should be practised or evaluated next, but it
// never executes generated tasks, changes protected evaluator inputs in place,
// promotes revisions, mutates canonical source, or participates in retrieval
// ranking.
package curriculum

const (
	EvidenceLifecycleFailure   = "lifecycle_failure"
	EvidenceFeedbackFailure    = "feedback_failure"
	EvidenceEvalWeakCase       = "eval_weak_case"
	EvidenceCoverageGap        = "coverage_gap"
	EvidenceCompatibilityGap   = "compatibility_failure"
	EvidenceEffectivePattern   = "effective_pattern"
	EvidenceTrustObserved      = "observed"
	EvidenceTrustUntrusted     = "untrusted"
	ProposalTrainingTask       = "training_task"
	ProposalDevelopmentEval    = "development_eval"
	ProposalProtectedEval      = "protected_eval"
	ProposalCapabilityGuidance = "capability_guidance"
	AudienceDevelopment        = "development"
	AudienceHeldOut            = "held_out"
	OracleDeterministic        = "deterministic"
	OracleReviewRequired       = "review_required"
	ReviewAccepted             = "accepted"
	ReviewRejected             = "rejected"

	MaxEvidence         = 64
	MaxProposalIDs      = 128
	MaxIdentityBytes    = 256
	MaxSummaryBytes     = 4 * 1024
	MaxReferenceBytes   = 2 * 1024
	MaxEnvironmentBytes = 512
)

type Scope struct {
	CapabilityID            string `json:"capability_id"`
	RevisionID              string `json:"revision_id"`
	TaskDistributionID      string `json:"task_distribution_id"`
	TaskDistributionVersion string `json:"task_distribution_version"`
	Environment             string `json:"environment"`
}

type EvidenceReference struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Summary   string `json:"summary,omitempty"`
	Trust     string `json:"trust"`
}

type CapabilityGap struct {
	ID              string              `json:"id"`
	OrganizationID  string              `json:"organization_id"`
	Scope           Scope               `json:"scope"`
	FailureKey      string              `json:"failure_key"`
	Evidence        []EvidenceReference `json:"evidence"`
	OccurrenceCount int                 `json:"occurrence_count"`
	ActorID         string              `json:"actor_id"`
	CorrelationID   string              `json:"correlation_id,omitempty"`
	CreatedAt       string              `json:"created_at"`
}

type RecordGapInput struct {
	OrganizationID string
	ActorID        string
	CorrelationID  string
	Scope          Scope
	FailureKey     string
	Evidence       []EvidenceReference
}

type Oracle struct {
	Mode      string `json:"mode"`
	Reference string `json:"reference,omitempty"`
	SHA256    string `json:"sha256,omitempty"`
}

type Proposal struct {
	ID                string `json:"id"`
	OrganizationID    string `json:"organization_id"`
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
	ProposalRevision  string `json:"proposal_revision"`
	ActorID           string `json:"actor_id"`
	CorrelationID     string `json:"correlation_id,omitempty"`
	CreatedAt         string `json:"created_at"`
}

type CreateProposalInput struct {
	OrganizationID    string
	ActorID           string
	CorrelationID     string
	GapID             string
	Name              string
	Version           string
	Kind              string
	Title             string
	Intent            string
	ArtifactReference string
	ArtifactSHA256    string
	Oracle            Oracle
}

type Review struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	ProposalID     string `json:"proposal_id"`
	Decision       string `json:"decision"`
	Reference      string `json:"reference"`
	ActorID        string `json:"actor_id"`
	CorrelationID  string `json:"correlation_id,omitempty"`
	CreatedAt      string `json:"created_at"`
}

type ReviewInput struct {
	OrganizationID string
	ActorID        string
	CorrelationID  string
	ProposalID     string
	Decision       string
	Reference      string
}

type EvalSuiteVersion struct {
	ID                     string   `json:"id"`
	OrganizationID         string   `json:"organization_id"`
	Name                   string   `json:"name"`
	Version                string   `json:"version"`
	ParentVersion          string   `json:"parent_version,omitempty"`
	DevelopmentProposalIDs []string `json:"development_proposal_ids"`
	HeldOutProposalIDs     []string `json:"held_out_proposal_ids"`
	SuiteRevision          string   `json:"suite_revision"`
	ActorID                string   `json:"actor_id"`
	CorrelationID          string   `json:"correlation_id,omitempty"`
	CreatedAt              string   `json:"created_at"`
}

type CreateEvalSuiteVersionInput struct {
	OrganizationID         string
	ActorID                string
	CorrelationID          string
	Name                   string
	Version                string
	ParentVersion          string
	DevelopmentProposalIDs []string
	HeldOutProposalIDs     []string
}

type HandoffProposal struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Version           string `json:"version"`
	Kind              string `json:"kind"`
	Title             string `json:"title"`
	Intent            string `json:"intent"`
	ArtifactReference string `json:"artifact_reference"`
	ArtifactSHA256    string `json:"artifact_sha256"`
	Oracle            Oracle `json:"oracle"`
}

type CandidateHandoff struct {
	SchemaVersion        string            `json:"schema_version"`
	ID                   string            `json:"id"`
	OrganizationID       string            `json:"organization_id"`
	GapID                string            `json:"gap_id"`
	Scope                Scope             `json:"scope"`
	Proposals            []HandoffProposal `json:"proposals"`
	ExcludedHeldOutCount int               `json:"excluded_held_out_count"`
}

type PrepareHandoffInput struct {
	OrganizationID string
	GapID          string
	ProposalIDs    []string
}
