// Package capabilityeval provides deterministic, machine-readable evaluation
// for scoped capability discovery. It deliberately evaluates compact routing
// descriptors only; package bodies and scope metadata never enter routing text.
package capabilityeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/search"
	"gopkg.in/yaml.v3"
)

const (
	CaseSingle   = "single"
	CaseMulti    = "multi"
	CaseNegative = "negative"
)

type Scope struct {
	Namespace  string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Repository string `yaml:"repository,omitempty" json:"repository,omitempty"`
}

type Document struct {
	RevisionID       string `yaml:"revision_id" json:"revision_id"`
	SkillID          string `yaml:"skill_id" json:"skill_id"`
	SourceRepository string `yaml:"source_repository" json:"source_repository"`
	Name             string `yaml:"name" json:"name"`
	Description      string `yaml:"description" json:"description"`
	Scope            Scope  `yaml:"scope,omitempty" json:"scope,omitempty"`
}

type Case struct {
	ID           string   `yaml:"id" json:"id"`
	Type         string   `yaml:"type" json:"type"`
	Query        string   `yaml:"query" json:"query"`
	RelevantIDs  []string `yaml:"relevant_ids" json:"relevant_ids"`
	Scope        Scope    `yaml:"scope,omitempty" json:"scope,omitempty"`
	ForbiddenIDs []string `yaml:"forbidden_ids,omitempty" json:"forbidden_ids,omitempty"`
}

type Thresholds struct {
	Top1                        float64 `yaml:"top1" json:"top1"`
	RecallAt3                   float64 `yaml:"recall_at_3" json:"recall_at_3"`
	MultiRecallAt5              float64 `yaml:"multi_recall_at_5" json:"multi_recall_at_5"`
	NegativeFalseActivationRate float64 `yaml:"negative_false_activation_rate" json:"negative_false_activation_rate"`
	ScopeLeakage                float64 `yaml:"scope_leakage" json:"scope_leakage"`
}

type Suite struct {
	Version    int        `yaml:"version" json:"version"`
	Name       string     `yaml:"name" json:"name"`
	Documents  []Document `yaml:"documents" json:"documents"`
	Cases      []Case     `yaml:"cases" json:"cases"`
	Thresholds Thresholds `yaml:"thresholds" json:"thresholds"`
}

type Metrics struct {
	Top1                        float64 `json:"top1"`
	RecallAt3                   float64 `json:"recall_at_3"`
	MultiRecallAt5              float64 `json:"multi_recall_at_5"`
	NegativeFalseActivationRate float64 `json:"negative_false_activation_rate"`
	ScopeLeakage                float64 `json:"scope_leakage"`
}

type CaseResult struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	ReturnedIDs []string `json:"returned_ids,omitempty"`
	Recall      float64  `json:"recall"`
	Top1Correct bool     `json:"top1_correct"`
	Activated   bool     `json:"activated"`
	LeakageIDs  []string `json:"leakage_ids,omitempty"`
	Degraded    bool     `json:"degraded"`
}

type Report struct {
	SchemaVersion  int          `json:"schema_version"`
	Suite          string       `json:"suite"`
	FixtureVersion int          `json:"fixture_version"`
	Passed         bool         `json:"passed"`
	Metrics        Metrics      `json:"metrics"`
	Thresholds     Thresholds   `json:"thresholds"`
	Cases          []CaseResult `json:"cases"`
	Failures       []string     `json:"failures,omitempty"`
}

func Load(path string) (Suite, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read scoped capability fixture: %w", err)
	}
	var suite Suite
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode scoped capability fixture: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

func (s Suite) Validate() error {
	if s.Version != 1 {
		return fmt.Errorf("unsupported scoped capability fixture version %d", s.Version)
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Documents) == 0 || len(s.Cases) == 0 {
		return fmt.Errorf("scoped capability fixture requires name, documents, and cases")
	}
	for name, value := range map[string]float64{
		"top1": s.Thresholds.Top1,
		"recall_at_3": s.Thresholds.RecallAt3,
		"multi_recall_at_5": s.Thresholds.MultiRecallAt5,
		"negative_false_activation_rate": s.Thresholds.NegativeFalseActivationRate,
		"scope_leakage": s.Thresholds.ScopeLeakage,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
			return fmt.Errorf("threshold %s must be between 0 and 1", name)
		}
	}
	ids := map[string]struct{}{}
	policies := map[string]Scope{}
	for _, doc := range s.Documents {
		if doc.RevisionID == "" || doc.SkillID == "" || doc.SourceRepository == "" || doc.Name == "" || doc.Description == "" {
			return fmt.Errorf("capability documents require revision_id, skill_id, source_repository, name, and description")
		}
		if _, exists := ids[doc.RevisionID]; exists {
			return fmt.Errorf("duplicate capability revision %q", doc.RevisionID)
		}
		ids[doc.RevisionID] = struct{}{}
		if doc.Scope.Namespace != "" || doc.Scope.Repository != "" {
			if doc.Scope.Namespace == "" || doc.Scope.Repository == "" {
				return fmt.Errorf("scoped source %q requires both namespace and repository", doc.SourceRepository)
			}
			if prior, exists := policies[doc.SourceRepository]; exists && prior != doc.Scope {
				return fmt.Errorf("source %q has conflicting scopes", doc.SourceRepository)
			}
			policies[doc.SourceRepository] = doc.Scope
		}
	}
	caseIDs := map[string]struct{}{}
	for _, c := range s.Cases {
		if c.ID == "" || strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("capability cases require id and query")
		}
		if _, exists := caseIDs[c.ID]; exists {
			return fmt.Errorf("duplicate capability case %q", c.ID)
		}
		caseIDs[c.ID] = struct{}{}
		switch c.Type {
		case CaseSingle:
			if len(c.RelevantIDs) != 1 {
				return fmt.Errorf("single case %q requires exactly one relevant id", c.ID)
			}
		case CaseMulti:
			if len(c.RelevantIDs) < 2 {
				return fmt.Errorf("multi case %q requires at least two relevant ids", c.ID)
			}
		case CaseNegative:
			if len(c.RelevantIDs) != 0 {
				return fmt.Errorf("negative case %q must not define relevant ids", c.ID)
			}
		default:
			return fmt.Errorf("case %q has unsupported type %q", c.ID, c.Type)
		}
		for _, id := range append(append([]string{}, c.RelevantIDs...), c.ForbiddenIDs...) {
			if _, exists := ids[id]; !exists {
				return fmt.Errorf("case %q references unknown revision %q", c.ID, id)
			}
		}
	}
	return nil
}

func Evaluate(s Suite) (Report, error) {
	if err := s.Validate(); err != nil {
		return Report{}, err
	}
	idx, err := search.New(nil)
	if err != nil {
		return Report{}, err
	}
	policyByRepo := map[string]capability.SourcePolicy{}
	for _, doc := range s.Documents {
		if err := idx.Add(search.Document{
			ID: doc.RevisionID, SkillID: doc.SkillID, OrganizationID: "eval", RepositoryID: doc.SourceRepository,
			Name: doc.Name, Description: doc.Description, TrustLevel: "approved", Searchable: true,
		}); err != nil {
			return Report{}, err
		}
		if doc.Scope.Namespace != "" || doc.Scope.Repository != "" {
			scope, err := capability.NewScope("eval", doc.Scope.Namespace, doc.Scope.Repository)
			if err != nil {
				return Report{}, err
			}
			policyByRepo[doc.SourceRepository] = capability.SourcePolicy{RepositoryID: doc.SourceRepository, Scope: scope}
		}
	}
	keys := make([]string, 0, len(policyByRepo))
	for key := range policyByRepo {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	policies := make([]capability.SourcePolicy, 0, len(keys))
	for _, key := range keys {
		policies = append(policies, policyByRepo[key])
	}
	service, err := capability.New(idx, policies)
	if err != nil {
		return Report{}, err
	}

	report := Report{SchemaVersion: 1, Suite: s.Name, FixtureVersion: s.Version, Passed: true, Thresholds: s.Thresholds, Cases: make([]CaseResult, 0, len(s.Cases))}
	var top1, recall3, multi5, negativeActivation, leakage float64
	var singles, multis, negatives, leakageChecks int
	for _, c := range s.Cases {
		scope, err := capability.NewScope("eval", c.Scope.Namespace, c.Scope.Repository)
		if err != nil {
			return Report{}, fmt.Errorf("case %s scope: %w", c.ID, err)
		}
		results, degraded, err := service.Search(c.Query, 50, 50, 5, 60, scope, search.Filters{})
		if err != nil {
			return Report{}, fmt.Errorf("case %s search: %w", c.ID, err)
		}
		result := CaseResult{ID: c.ID, Type: c.Type, Degraded: degraded}
		for _, candidate := range results {
			result.ReturnedIDs = append(result.ReturnedIDs, candidate.Capability.Provenance.RevisionID)
		}
		for _, forbidden := range c.ForbiddenIDs {
			leakageChecks++
			if contains(result.ReturnedIDs, forbidden) {
				leakage++
				result.LeakageIDs = append(result.LeakageIDs, forbidden)
			}
		}
		switch c.Type {
		case CaseSingle:
			singles++
			result.Recall = recall(result.ReturnedIDs, c.RelevantIDs, 3)
			result.Top1Correct = len(result.ReturnedIDs) > 0 && result.ReturnedIDs[0] == c.RelevantIDs[0]
			if result.Top1Correct {
				top1++
			}
			recall3 += result.Recall
		case CaseMulti:
			multis++
			result.Recall = recall(result.ReturnedIDs, c.RelevantIDs, 5)
			multi5 += result.Recall
		case CaseNegative:
			negatives++
			result.Activated = len(result.ReturnedIDs) > 0
			if result.Activated {
				negativeActivation++
			}
		}
		report.Cases = append(report.Cases, result)
	}
	report.Metrics = Metrics{
		Top1: ratio(top1, singles),
		RecallAt3: ratio(recall3, singles),
		MultiRecallAt5: ratio(multi5, multis),
		NegativeFalseActivationRate: ratio(negativeActivation, negatives),
		ScopeLeakage: ratio(leakage, leakageChecks),
	}
	if report.Metrics.Top1 < s.Thresholds.Top1 {
		report.Failures = append(report.Failures, fmt.Sprintf("top1 %.4f is below %.4f", report.Metrics.Top1, s.Thresholds.Top1))
	}
	if report.Metrics.RecallAt3 < s.Thresholds.RecallAt3 {
		report.Failures = append(report.Failures, fmt.Sprintf("recall@3 %.4f is below %.4f", report.Metrics.RecallAt3, s.Thresholds.RecallAt3))
	}
	if report.Metrics.MultiRecallAt5 < s.Thresholds.MultiRecallAt5 {
		report.Failures = append(report.Failures, fmt.Sprintf("multi recall@5 %.4f is below %.4f", report.Metrics.MultiRecallAt5, s.Thresholds.MultiRecallAt5))
	}
	if report.Metrics.NegativeFalseActivationRate > s.Thresholds.NegativeFalseActivationRate {
		report.Failures = append(report.Failures, fmt.Sprintf("negative false activation %.4f exceeds %.4f", report.Metrics.NegativeFalseActivationRate, s.Thresholds.NegativeFalseActivationRate))
	}
	if report.Metrics.ScopeLeakage > s.Thresholds.ScopeLeakage {
		report.Failures = append(report.Failures, fmt.Sprintf("scope leakage %.4f exceeds %.4f", report.Metrics.ScopeLeakage, s.Thresholds.ScopeLeakage))
	}
	report.Passed = len(report.Failures) == 0
	return report, nil
}

func EvaluateFile(path string) (Report, error) {
	suite, err := Load(path)
	if err != nil {
		return Report{}, err
	}
	return Evaluate(suite)
}

func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

func recall(returned, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(returned) {
		k = len(returned)
	}
	found := 0
	for _, expected := range relevant {
		if contains(returned[:k], expected) {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ratio(value float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return value / float64(count)
}
