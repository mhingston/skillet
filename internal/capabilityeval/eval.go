// Package capabilityeval provides deterministic, machine-readable evaluation
// for scoped capability discovery. It evaluates compact routing descriptors
// only; full skill bodies and MCP tool schemas never enter routing text.
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
	RevisionID       string          `yaml:"revision_id" json:"revision_id"`
	SkillID          string          `yaml:"skill_id" json:"skill_id"`
	Kind             capability.Kind `yaml:"kind,omitempty" json:"kind,omitempty"`
	SourceRepository string          `yaml:"source_repository" json:"source_repository"`
	Name             string          `yaml:"name" json:"name"`
	Description      string          `yaml:"description" json:"description"`
	Scope            Scope           `yaml:"scope,omitempty" json:"scope,omitempty"`
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
	Top1                        float64            `yaml:"top1" json:"top1"`
	RecallAt3                   float64            `yaml:"recall_at_3" json:"recall_at_3"`
	MultiRecallAt5              float64            `yaml:"multi_recall_at_5" json:"multi_recall_at_5"`
	NegativeFalseActivationRate float64            `yaml:"negative_false_activation_rate" json:"negative_false_activation_rate"`
	ScopeLeakage                float64            `yaml:"scope_leakage" json:"scope_leakage"`
	RecallByKind                map[string]float64 `yaml:"recall_by_kind,omitempty" json:"recall_by_kind,omitempty"`
}

type Suite struct {
	Version    int        `yaml:"version" json:"version"`
	Name       string     `yaml:"name" json:"name"`
	Documents  []Document `yaml:"documents" json:"documents"`
	Cases      []Case     `yaml:"cases" json:"cases"`
	Thresholds Thresholds `yaml:"thresholds" json:"thresholds"`
}

type Metrics struct {
	Top1                        float64                   `json:"top1"`
	RecallAt3                   float64                   `json:"recall_at_3"`
	MultiRecallAt5              float64                   `json:"multi_recall_at_5"`
	NegativeFalseActivationRate float64                   `json:"negative_false_activation_rate"`
	ScopeLeakage                float64                   `json:"scope_leakage"`
	RecallByKind                map[string]float64        `json:"recall_by_kind"`
	KindConfusion               map[string]map[string]int `json:"kind_confusion"`
}

type CaseResult struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	ReturnedIDs   []string          `json:"returned_ids,omitempty"`
	ReturnedKinds []capability.Kind `json:"returned_kinds,omitempty"`
	Recall        float64           `json:"recall"`
	Top1Correct   bool              `json:"top1_correct"`
	Activated     bool              `json:"activated"`
	LeakageIDs    []string          `json:"leakage_ids,omitempty"`
	Degraded      bool              `json:"degraded"`
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
	if s.Version != 1 && s.Version != 2 {
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
		if err := validateThreshold(name, value); err != nil {
			return err
		}
	}
	for kind, value := range s.Thresholds.RecallByKind {
		if !validKind(capability.Kind(kind)) {
			return fmt.Errorf("recall_by_kind has unsupported kind %q", kind)
		}
		if err := validateThreshold("recall_by_kind."+kind, value); err != nil {
			return err
		}
	}

	ids := map[string]struct{}{}
	policies := map[string]Scope{}
	for _, doc := range s.Documents {
		kind := documentKind(doc)
		if doc.RevisionID == "" || doc.SkillID == "" || doc.SourceRepository == "" || doc.Name == "" || doc.Description == "" {
			return fmt.Errorf("capability documents require revision_id, skill_id, source_repository, name, and description")
		}
		if !validKind(kind) {
			return fmt.Errorf("capability revision %q has unsupported kind %q", doc.RevisionID, kind)
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
	kindByRevision := map[string]capability.Kind{}
	detailOverrides := make([]capability.Detail, 0)
	for _, doc := range s.Documents {
		kind := documentKind(doc)
		kindByRevision[doc.RevisionID] = kind
		if err := idx.Add(search.Document{
			ID: doc.RevisionID, SkillID: doc.SkillID, OrganizationID: "eval", RepositoryID: doc.SourceRepository,
			Name: doc.Name, Description: doc.Description, TrustLevel: "approved", Searchable: true,
		}); err != nil {
			return Report{}, err
		}
		docScope := capability.Scope{Organization: "eval"}
		if doc.Scope.Namespace != "" || doc.Scope.Repository != "" {
			scope, err := capability.NewScope("eval", doc.Scope.Namespace, doc.Scope.Repository)
			if err != nil {
				return Report{}, err
			}
			docScope = scope
			policyByRepo[doc.SourceRepository] = capability.SourcePolicy{RepositoryID: doc.SourceRepository, Scope: scope}
		}
		if kind != capability.KindSkill {
			detail := capability.Detail{Descriptor: capability.Descriptor{
				Identity:    capability.Identity{ID: doc.SkillID, Kind: kind},
				Name:        doc.Name,
				Description: doc.Description,
				Scope:       docScope,
				Source:      capability.Source{RepositoryID: doc.SourceRepository},
				Provenance:  capability.Provenance{RevisionID: doc.RevisionID},
				TrustLevel:  "approved",
				Status:      capability.StatusActive,
			}}
			if kind == capability.KindTool {
				detail.Tool = &capability.ToolDetail{
					Name: doc.Name, InputSchema: map[string]any{"type": "object"},
					InputSchemaSummary: "type=object", InputSchemaSHA256: "eval", UntrustedMetadata: true,
				}
			}
			detailOverrides = append(detailOverrides, detail)
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
	if err := service.RegisterDetails(detailOverrides); err != nil {
		return Report{}, err
	}

	report := Report{
		SchemaVersion: 2, Suite: s.Name, FixtureVersion: s.Version, Passed: true, Thresholds: s.Thresholds,
		Cases: make([]CaseResult, 0, len(s.Cases)),
		Metrics: Metrics{RecallByKind: map[string]float64{}, KindConfusion: map[string]map[string]int{}},
	}
	var top1, recall3, multi5, negativeActivation, leakage float64
	var singles, multis, negatives, leakageChecks int
	kindRelevant := map[string]float64{}
	kindFound := map[string]float64{}
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
			result.ReturnedKinds = append(result.ReturnedKinds, candidate.Capability.Identity.Kind)
		}
		for _, forbidden := range c.ForbiddenIDs {
			leakageChecks++
			if contains(result.ReturnedIDs, forbidden) {
				leakage++
				result.LeakageIDs = append(result.LeakageIDs, forbidden)
			}
		}

		recallK := 0
		switch c.Type {
		case CaseSingle:
			singles++
			recallK = 3
			result.Recall = recall(result.ReturnedIDs, c.RelevantIDs, recallK)
			result.Top1Correct = len(result.ReturnedIDs) > 0 && result.ReturnedIDs[0] == c.RelevantIDs[0]
			if result.Top1Correct {
				top1++
			}
			recall3 += result.Recall
			recordConfusion(report.Metrics.KindConfusion, string(kindByRevision[c.RelevantIDs[0]]), topKind(result.ReturnedKinds))
		case CaseMulti:
			multis++
			recallK = 5
			result.Recall = recall(result.ReturnedIDs, c.RelevantIDs, recallK)
			multi5 += result.Recall
		case CaseNegative:
			negatives++
			result.Activated = len(result.ReturnedIDs) > 0
			if result.Activated {
				negativeActivation++
			}
			recordConfusion(report.Metrics.KindConfusion, "none", topKind(result.ReturnedKinds))
		}
		if recallK > 0 {
			for _, relevantID := range c.RelevantIDs {
				kind := string(kindByRevision[relevantID])
				kindRelevant[kind]++
				if contains(prefix(result.ReturnedIDs, recallK), relevantID) {
					kindFound[kind]++
				}
			}
		}
		report.Cases = append(report.Cases, result)
	}
	report.Metrics.Top1 = ratio(top1, singles)
	report.Metrics.RecallAt3 = ratio(recall3, singles)
	report.Metrics.MultiRecallAt5 = ratio(multi5, multis)
	report.Metrics.NegativeFalseActivationRate = ratio(negativeActivation, negatives)
	report.Metrics.ScopeLeakage = ratio(leakage, leakageChecks)
	for _, kind := range []capability.Kind{capability.KindSkill, capability.KindPlaybook, capability.KindTool} {
		name := string(kind)
		if kindRelevant[name] == 0 {
			continue
		}
		report.Metrics.RecallByKind[name] = kindFound[name] / kindRelevant[name]
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
	kindThresholds := make([]string, 0, len(s.Thresholds.RecallByKind))
	for kind := range s.Thresholds.RecallByKind {
		kindThresholds = append(kindThresholds, kind)
	}
	sort.Strings(kindThresholds)
	for _, kind := range kindThresholds {
		actual := report.Metrics.RecallByKind[kind]
		threshold := s.Thresholds.RecallByKind[kind]
		if actual < threshold {
			report.Failures = append(report.Failures, fmt.Sprintf("%s recall %.4f is below %.4f", kind, actual, threshold))
		}
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

func validateThreshold(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("threshold %s must be between 0 and 1", name)
	}
	return nil
}

func documentKind(doc Document) capability.Kind {
	if doc.Kind == "" {
		return capability.KindSkill
	}
	return doc.Kind
}

func validKind(kind capability.Kind) bool {
	return kind == capability.KindSkill || kind == capability.KindPlaybook || kind == capability.KindTool
}

func topKind(kinds []capability.Kind) string {
	if len(kinds) == 0 {
		return "none"
	}
	return string(kinds[0])
}

func recordConfusion(matrix map[string]map[string]int, expected, actual string) {
	if matrix[expected] == nil {
		matrix[expected] = map[string]int{}
	}
	matrix[expected][actual]++
}

func prefix(values []string, k int) []string {
	if k < 0 {
		return nil
	}
	if k > len(values) {
		k = len(values)
	}
	return values[:k]
}

func recall(returned, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	found := 0
	for _, expected := range relevant {
		if contains(prefix(returned, k), expected) {
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
