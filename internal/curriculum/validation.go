package curriculum

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (s *Store) normalizeScope(ctx context.Context, organizationID string, scope Scope) (Scope, error) {
	scope.CapabilityID = strings.TrimSpace(scope.CapabilityID)
	scope.RevisionID = strings.TrimSpace(scope.RevisionID)
	scope.TaskDistributionID = strings.TrimSpace(scope.TaskDistributionID)
	scope.TaskDistributionVersion = strings.TrimSpace(scope.TaskDistributionVersion)
	scope.Environment = strings.TrimSpace(scope.Environment)
	if scope.CapabilityID == "" || scope.RevisionID == "" || scope.TaskDistributionID == "" || scope.TaskDistributionVersion == "" || scope.Environment == "" {
		return Scope{}, fmt.Errorf("gap scope requires capability, exact revision, task-distribution identity/version, and environment")
	}
	for field, value := range map[string]string{"capability_id": scope.CapabilityID, "revision_id": scope.RevisionID, "task_distribution_id": scope.TaskDistributionID, "task_distribution_version": scope.TaskDistributionVersion} {
		if err := validateText(field, value, MaxIdentityBytes); err != nil {
			return Scope{}, err
		}
	}
	if err := validateText("environment", scope.Environment, MaxEnvironmentBytes); err != nil {
		return Scope{}, err
	}
	revision, err := s.Catalogue.Revision(ctx, organizationID, scope.RevisionID)
	if err != nil {
		return Scope{}, err
	}
	if revision.SkillID != scope.CapabilityID {
		return Scope{}, fmt.Errorf("revision %s belongs to capability %s, not %s", scope.RevisionID, revision.SkillID, scope.CapabilityID)
	}
	return scope, nil
}

func normalizeEvidence(items []EvidenceReference) ([]EvidenceReference, error) {
	if len(items) > MaxEvidence {
		return nil, fmt.Errorf("too many evidence references: %d > %d", len(items), MaxEvidence)
	}
	out := make([]EvidenceReference, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		item.Kind = strings.TrimSpace(item.Kind)
		item.Reference = strings.TrimSpace(item.Reference)
		item.Summary = strings.TrimSpace(item.Summary)
		if !supportedEvidenceKind(item.Kind) {
			return nil, fmt.Errorf("unsupported curriculum evidence kind %q", item.Kind)
		}
		if item.Reference == "" {
			return nil, fmt.Errorf("evidence reference is required")
		}
		if err := validateText("evidence reference", item.Reference, MaxReferenceBytes); err != nil {
			return nil, err
		}
		if err := validateText("evidence summary", item.Summary, MaxSummaryBytes); err != nil {
			return nil, err
		}
		if item.Kind == EvidenceFeedbackFailure {
			item.Trust = EvidenceTrustUntrusted
		} else {
			item.Trust = EvidenceTrustObserved
		}
		key := item.Kind + "\x00" + item.Reference
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Reference < out[j].Reference
		}
		return out[i].Kind < out[j].Kind
	})
	return out, nil
}

func supportedEvidenceKind(kind string) bool {
	switch kind {
	case EvidenceLifecycleFailure, EvidenceFeedbackFailure, EvidenceEvalWeakCase, EvidenceCoverageGap, EvidenceCompatibilityGap, EvidenceEffectivePattern:
		return true
	default:
		return false
	}
}

func requiresRepeatedEvidence(kind string) bool {
	switch kind {
	case EvidenceLifecycleFailure, EvidenceFeedbackFailure, EvidenceEvalWeakCase, EvidenceCompatibilityGap:
		return true
	default:
		return false
	}
}

func audienceForKind(kind string) (string, error) {
	switch kind {
	case ProposalTrainingTask, ProposalDevelopmentEval, ProposalCapabilityGuidance:
		return AudienceDevelopment, nil
	case ProposalProtectedEval:
		return AudienceHeldOut, nil
	default:
		return "", fmt.Errorf("unsupported curriculum proposal kind %q", kind)
	}
}

func normalizeOracle(oracle Oracle) (Oracle, error) {
	oracle.Mode = strings.TrimSpace(oracle.Mode)
	oracle.Reference = strings.TrimSpace(oracle.Reference)
	oracle.SHA256 = strings.ToLower(strings.TrimSpace(oracle.SHA256))
	switch oracle.Mode {
	case OracleDeterministic:
		if oracle.Reference == "" || !digestPattern.MatchString(oracle.SHA256) {
			return Oracle{}, fmt.Errorf("deterministic oracle requires a reference and lowercase SHA-256 digest")
		}
		if err := validateText("oracle reference", oracle.Reference, MaxReferenceBytes); err != nil {
			return Oracle{}, err
		}
	case OracleReviewRequired:
		if oracle.Reference != "" {
			if err := validateText("review-path reference", oracle.Reference, MaxReferenceBytes); err != nil {
				return Oracle{}, err
			}
		}
		if oracle.SHA256 != "" && !digestPattern.MatchString(oracle.SHA256) {
			return Oracle{}, fmt.Errorf("oracle sha256 must be empty or a lowercase 64-character digest")
		}
	default:
		return Oracle{}, fmt.Errorf("oracle mode must be %q or %q", OracleDeterministic, OracleReviewRequired)
	}
	return oracle, nil
}

func normalizeIDs(ids []string) ([]string, error) {
	if len(ids) > MaxProposalIDs {
		return nil, fmt.Errorf("too many proposal ids: %d > %d", len(ids), MaxProposalIDs)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("proposal ids cannot be empty")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func overlaps(a, b []string) bool {
	seen := map[string]bool{}
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if seen[id] {
			return true
		}
	}
	return false
}

func containsAll(current, required []string) bool {
	set := map[string]bool{}
	for _, id := range current {
		set[id] = true
	}
	for _, id := range required {
		if !set[id] {
			return false
		}
	}
	return true
}

func validateText(field, value string, max int) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", field)
	}
	if len(value) > max {
		return fmt.Errorf("%s exceeds %d bytes", field, max)
	}
	return nil
}

func revisionDigest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contentID(prefix, organizationID string, payload []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(organizationID)))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payload)
	return prefix + hex.EncodeToString(h.Sum(nil))
}
