// Package eval provides a deterministic retrieval-evaluation harness.
//
// Evaluation fixtures contain routing documents and synthetic embedding
// vectors only. They do not contain skill bodies, Git data, or provider
// credentials. The harness can therefore exercise the same search.Index used
// by the service without making network calls or depending on a live source
// repository.
package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"

	"github.com/mhingston/skillet/internal/search"
	"gopkg.in/yaml.v3"
)

const (
	CaseSingle     = "single"
	CaseParaphrase = "paraphrase"
	CaseNegative   = "negative"
	CaseMulti      = "multi"
)

// Suite is the versioned, machine-readable input to the evaluator.
type Suite struct {
	Version    int        `yaml:"version" json:"version"`
	Name       string     `yaml:"name" json:"name"`
	Documents  []Document `yaml:"documents" json:"documents"`
	Cases      []Case     `yaml:"cases" json:"cases"`
	Thresholds Thresholds `yaml:"thresholds" json:"thresholds"`
}

// Document is deliberately limited to a routing document. In particular,
// there is no field for SKILL.md or any other package content.
type Document struct {
	ID            string            `yaml:"id" json:"id"`
	Name          string            `yaml:"name" json:"name"`
	Description   string            `yaml:"description" json:"description"`
	Compatibility string            `yaml:"compatibility,omitempty" json:"compatibility,omitempty"`
	Organization  string            `yaml:"organization_id,omitempty" json:"organization_id,omitempty"`
	Repository    string            `yaml:"repository_id,omitempty" json:"repository_id,omitempty"`
	TrustLevel    string            `yaml:"trust_level,omitempty" json:"trust_level,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty" json:"metadata,omitempty"`
	Vector        []float32         `yaml:"vector" json:"vector"`
}

// Case is one judged query. relevant_ids is empty only for a negative case.
type Case struct {
	ID          string    `yaml:"id" json:"id"`
	Type        string    `yaml:"type" json:"type"`
	Query       string    `yaml:"query" json:"query"`
	QueryVector []float32 `yaml:"query_vector" json:"query_vector"`
	RelevantIDs []string  `yaml:"relevant_ids" json:"relevant_ids"`
}

// Thresholds are ratios in [0,1], not percentages.
type Thresholds struct {
	SingleTop1Accuracy          float64 `yaml:"single_top1_accuracy" json:"single_top1_accuracy"`
	SingleRecallAt3             float64 `yaml:"single_recall_at3" json:"single_recall_at3"`
	MultiRecallAt5              float64 `yaml:"multi_recall_at5" json:"multi_recall_at5"`
	NegativeFalseActivationRate float64 `yaml:"negative_false_activation_rate" json:"negative_false_activation_rate"`
	MaxRegression               float64 `yaml:"max_regression" json:"max_regression"`
}

// Config controls deterministic search depths and the negative-query
// evidence rule. NegativeActivationScore is an RRF evidence threshold, not a
// calibrated probability. It is intentionally explicit so the evaluator
// never presents raw retrieval scores as confidence.
type Config struct {
	LexicalDepth            int
	VectorDepth             int
	RRFK                    int
	SingleRecallK           int
	MultiRecallK            int
	NegativeLimit           int
	NegativeActivationScore float64
	Baseline                *Report
}

// Searcher is the narrow seam needed by the evaluator. *search.Index
// satisfies it, while tests can provide a deterministic fake.
type Searcher interface {
	Search(query string, lexicalDepth, vectorDepth, limit, rrfK int) ([]search.Hit, bool, error)
}

// Metrics contains the aggregate retrieval metrics reported by Evaluate.
type Metrics struct {
	SingleTop1Accuracy          float64 `json:"single_top1_accuracy"`
	SingleRecallAt3             float64 `json:"single_recall_at3"`
	MultiRecallAt5              float64 `json:"multi_recall_at5"`
	NegativeFalseActivationRate float64 `json:"negative_false_activation_rate"`
}

// CaseResult preserves enough evidence to inspect a failed evaluation
// without rerunning the search. IDs are sorted only where ordering is not
// meaningful; returned IDs retain ranking order.
type CaseResult struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	RelevantIDs []string `json:"relevant_ids,omitempty"`
	ReturnedIDs []string `json:"returned_ids,omitempty"`
	Recall      float64  `json:"recall"`
	Top1Correct bool     `json:"top1_correct"`
	Activated   bool     `json:"activated"`
	Degraded    bool     `json:"degraded"`
	Error       string   `json:"error,omitempty"`
}

// Report is stable JSON output suitable for CI artifacts and baseline
// comparisons.
type Report struct {
	SchemaVersion int          `json:"schema_version"`
	Suite         string       `json:"suite"`
	Passed        bool         `json:"passed"`
	Thresholds    Thresholds   `json:"thresholds"`
	Metrics       Metrics      `json:"metrics"`
	Cases         []CaseResult `json:"cases"`
	Failures      []string     `json:"failures,omitempty"`
}

// Load parses and validates a YAML or JSON suite.
func Load(r io.Reader) (Suite, error) {
	var suite Suite
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("decode retrieval suite: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return Suite{}, err
	}
	return suite, nil
}

// LoadFile loads a suite from a local fixture file.
func LoadFile(path string) (Suite, error) {
	f, err := os.Open(path)
	if err != nil {
		return Suite{}, fmt.Errorf("open retrieval suite %q: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}

// Validate checks fixture structure and prevents accidental inclusion of
// package contents in the evaluation corpus.
func (s Suite) Validate() error {
	if s.Version != 1 {
		return fmt.Errorf("unsupported retrieval suite version %d", s.Version)
	}
	if strings.TrimSpace(s.Name) == "" {
		return fmt.Errorf("retrieval suite name is required")
	}
	if len(s.Documents) == 0 {
		return fmt.Errorf("retrieval suite requires documents")
	}
	ids := make(map[string]struct{}, len(s.Documents))
	dimension := 0
	for _, doc := range s.Documents {
		if strings.TrimSpace(doc.ID) == "" {
			return fmt.Errorf("document id is required")
		}
		if _, exists := ids[doc.ID]; exists {
			return fmt.Errorf("duplicate document id %q", doc.ID)
		}
		ids[doc.ID] = struct{}{}
		if strings.TrimSpace(doc.Name) == "" || strings.TrimSpace(doc.Description) == "" {
			return fmt.Errorf("document %q requires name and description", doc.ID)
		}
		if len(doc.Vector) == 0 {
			return fmt.Errorf("document %q requires a synthetic vector", doc.ID)
		}
		if dimension == 0 {
			dimension = len(doc.Vector)
		}
		if len(doc.Vector) != dimension {
			return fmt.Errorf("document %q vector dimension %d does not match %d", doc.ID, len(doc.Vector), dimension)
		}
		if !finiteVector(doc.Vector) {
			return fmt.Errorf("document %q vector contains a non-finite value", doc.ID)
		}
	}
	caseIDs := make(map[string]struct{}, len(s.Cases))
	queryVectors := make(map[string][]float32, len(s.Cases))
	for _, c := range s.Cases {
		if strings.TrimSpace(c.ID) == "" {
			return fmt.Errorf("case id is required")
		}
		if _, exists := caseIDs[c.ID]; exists {
			return fmt.Errorf("duplicate case id %q", c.ID)
		}
		caseIDs[c.ID] = struct{}{}
		if !validCaseType(c.Type) {
			return fmt.Errorf("case %q has unsupported type %q", c.ID, c.Type)
		}
		if strings.TrimSpace(c.Query) == "" {
			return fmt.Errorf("case %q query is required", c.ID)
		}
		if len(c.QueryVector) != dimension || !finiteVector(c.QueryVector) {
			return fmt.Errorf("case %q query_vector must have dimension %d and finite values", c.ID, dimension)
		}
		if prior, exists := queryVectors[c.Query]; exists && !vectorsEqual(prior, c.QueryVector) {
			return fmt.Errorf("case %q has a conflicting query vector for query %q", c.ID, c.Query)
		}
		queryVectors[c.Query] = append([]float32(nil), c.QueryVector...)
		seenRelevant := map[string]struct{}{}
		for _, id := range c.RelevantIDs {
			if _, exists := ids[id]; !exists {
				return fmt.Errorf("case %q references unknown document %q", c.ID, id)
			}
			if _, exists := seenRelevant[id]; exists {
				return fmt.Errorf("case %q repeats relevant document %q", c.ID, id)
			}
			seenRelevant[id] = struct{}{}
		}
		switch c.Type {
		case CaseNegative:
			if len(c.RelevantIDs) != 0 {
				return fmt.Errorf("negative case %q must have no relevant_ids", c.ID)
			}
		case CaseSingle, CaseParaphrase:
			if len(c.RelevantIDs) != 1 {
				return fmt.Errorf("%s case %q must have exactly one relevant_id", c.Type, c.ID)
			}
		case CaseMulti:
			if len(c.RelevantIDs) < 2 {
				return fmt.Errorf("multi case %q must have at least two relevant_ids", c.ID)
			}
		}
	}
	if err := validateRatio("single_top1_accuracy", s.Thresholds.SingleTop1Accuracy); err != nil {
		return err
	}
	if err := validateRatio("single_recall_at3", s.Thresholds.SingleRecallAt3); err != nil {
		return err
	}
	if err := validateRatio("multi_recall_at5", s.Thresholds.MultiRecallAt5); err != nil {
		return err
	}
	if err := validateRatio("negative_false_activation_rate", s.Thresholds.NegativeFalseActivationRate); err != nil {
		return err
	}
	return validateRatio("max_regression", s.Thresholds.MaxRegression)
}

// RequireCoverage enforces the handoff's initial evaluation mix. It is kept
// separate from Validate so small unit suites can test metric math.
func RequireCoverage(s Suite) error {
	counts := map[string]int{}
	for _, c := range s.Cases {
		counts[c.Type]++
	}
	required := map[string]int{CaseSingle: 20, CaseParaphrase: 10, CaseNegative: 10, CaseMulti: 10}
	for _, typ := range []string{CaseSingle, CaseParaphrase, CaseNegative, CaseMulti} {
		if counts[typ] < required[typ] {
			return fmt.Errorf("retrieval suite requires at least %d %s cases, found %d", required[typ], typ, counts[typ])
		}
	}
	return nil
}

// NewSyntheticIndex builds the actual search implementation used by the
// fixture runner. The embedder is an exact query-to-vector lookup, so no
// network provider or model can change the result.
func NewSyntheticIndex(s Suite) (*search.Index, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	vectors := make(map[string][]float32, len(s.Cases))
	for _, c := range s.Cases {
		vectors[c.Query] = append([]float32(nil), c.QueryVector...)
	}
	idx, err := search.New(syntheticEmbedder{vectors: vectors})
	if err != nil {
		return nil, fmt.Errorf("create synthetic retrieval index: %w", err)
	}
	for _, doc := range s.Documents {
		if err := idx.Add(search.Document{
			ID:             doc.ID,
			Name:           doc.Name,
			Description:    doc.Description,
			Compatibility:  doc.Compatibility,
			OrganizationID: doc.Organization,
			RepositoryID:   doc.Repository,
			TrustLevel:     doc.TrustLevel,
			Metadata:       cloneMetadata(doc.Metadata),
			Vector:         append([]float32(nil), doc.Vector...),
			Searchable:     true,
		}); err != nil {
			return nil, fmt.Errorf("index synthetic document %q: %w", doc.ID, err)
		}
	}
	return idx, nil
}

// Evaluate runs every case and returns a report even when gates fail. Search
// execution errors are recorded in the report so CI receives a useful
// machine-readable artifact.
func Evaluate(s Suite, searcher Searcher, cfg Config) (Report, error) {
	if err := s.Validate(); err != nil {
		return Report{}, err
	}
	if searcher == nil {
		return Report{}, fmt.Errorf("retrieval searcher is required")
	}
	cfg = withDefaults(cfg)
	report := Report{SchemaVersion: 1, Suite: s.Name, Thresholds: s.Thresholds, Passed: true, Cases: make([]CaseResult, 0, len(s.Cases))}
	var singleTop1, singleRecall, multiRecall, negativeActivation float64
	var singleCount, multiCount, negativeCount int
	for _, c := range s.Cases {
		limit := cfg.MultiRecallK
		if limit < cfg.SingleRecallK {
			limit = cfg.SingleRecallK
		}
		if limit < cfg.NegativeLimit {
			limit = cfg.NegativeLimit
		}
		hits, degraded, err := searcher.Search(c.Query, cfg.LexicalDepth, cfg.VectorDepth, limit, cfg.RRFK)
		result := CaseResult{ID: c.ID, Type: c.Type, RelevantIDs: append([]string(nil), c.RelevantIDs...), Degraded: degraded}
		for _, hit := range hits {
			result.ReturnedIDs = append(result.ReturnedIDs, hit.ID)
		}
		if err != nil {
			result.Error = err.Error()
			report.Failures = append(report.Failures, fmt.Sprintf("case %s: search failed: %v", c.ID, err))
			report.Cases = append(report.Cases, result)
			continue
		}
		result.Recall = recallAt(result.ReturnedIDs, c.RelevantIDs, recallK(c.Type, cfg))
		result.Top1Correct = len(c.RelevantIDs) == 1 && len(result.ReturnedIDs) > 0 && result.ReturnedIDs[0] == c.RelevantIDs[0]
		if c.Type == CaseNegative {
			result.Activated = activated(hits, cfg.NegativeActivationScore)
		}
		switch c.Type {
		case CaseSingle, CaseParaphrase:
			singleCount++
			if result.Top1Correct {
				singleTop1++
			}
			singleRecall += result.Recall
		case CaseMulti:
			multiCount++
			multiRecall += result.Recall
		case CaseNegative:
			negativeCount++
			if result.Activated {
				negativeActivation++
			}
		}
		report.Cases = append(report.Cases, result)
	}
	if singleCount > 0 {
		report.Metrics.SingleTop1Accuracy = singleTop1 / float64(singleCount)
		report.Metrics.SingleRecallAt3 = singleRecall / float64(singleCount)
	}
	if multiCount > 0 {
		report.Metrics.MultiRecallAt5 = multiRecall / float64(multiCount)
	}
	if negativeCount > 0 {
		report.Metrics.NegativeFalseActivationRate = negativeActivation / float64(negativeCount)
	}
	applyGates(&report, s.Thresholds, cfg.Baseline, singleCount > 0, multiCount > 0, negativeCount > 0)
	return report, nil
}

func applyGates(report *Report, thresholds Thresholds, baseline *Report, hasSingle, hasMulti, hasNegative bool) {
	if hasSingle && report.Metrics.SingleTop1Accuracy < thresholds.SingleTop1Accuracy {
		report.Failures = append(report.Failures, fmt.Sprintf("single top-1 accuracy %.4f is below %.4f", report.Metrics.SingleTop1Accuracy, thresholds.SingleTop1Accuracy))
	}
	if hasSingle && report.Metrics.SingleRecallAt3 < thresholds.SingleRecallAt3 {
		report.Failures = append(report.Failures, fmt.Sprintf("single recall@3 %.4f is below %.4f", report.Metrics.SingleRecallAt3, thresholds.SingleRecallAt3))
	}
	if hasMulti && report.Metrics.MultiRecallAt5 < thresholds.MultiRecallAt5 {
		report.Failures = append(report.Failures, fmt.Sprintf("multi recall@5 %.4f is below %.4f", report.Metrics.MultiRecallAt5, thresholds.MultiRecallAt5))
	}
	if hasNegative && report.Metrics.NegativeFalseActivationRate > thresholds.NegativeFalseActivationRate {
		report.Failures = append(report.Failures, fmt.Sprintf("negative false-activation rate %.4f exceeds %.4f", report.Metrics.NegativeFalseActivationRate, thresholds.NegativeFalseActivationRate))
	}
	if baseline != nil {
		maxDrop := thresholds.MaxRegression
		if report.Metrics.SingleTop1Accuracy < baseline.Metrics.SingleTop1Accuracy-maxDrop {
			report.Failures = append(report.Failures, "single top-1 accuracy regressed beyond baseline allowance")
		}
		if report.Metrics.SingleRecallAt3 < baseline.Metrics.SingleRecallAt3-maxDrop {
			report.Failures = append(report.Failures, "single recall@3 regressed beyond baseline allowance")
		}
		if report.Metrics.MultiRecallAt5 < baseline.Metrics.MultiRecallAt5-maxDrop {
			report.Failures = append(report.Failures, "multi recall@5 regressed beyond baseline allowance")
		}
		if report.Metrics.NegativeFalseActivationRate > baseline.Metrics.NegativeFalseActivationRate+maxDrop {
			report.Failures = append(report.Failures, "negative false-activation rate regressed beyond baseline allowance")
		}
	}
	report.Passed = len(report.Failures) == 0
}

func withDefaults(cfg Config) Config {
	if cfg.LexicalDepth < 1 {
		cfg.LexicalDepth = 50
	}
	if cfg.VectorDepth < 1 {
		cfg.VectorDepth = 50
	}
	if cfg.RRFK < 1 {
		cfg.RRFK = 60
	}
	if cfg.SingleRecallK < 1 {
		cfg.SingleRecallK = 3
	}
	if cfg.MultiRecallK < 1 {
		cfg.MultiRecallK = 5
	}
	if cfg.NegativeLimit < 1 {
		cfg.NegativeLimit = 5
	}
	if cfg.NegativeActivationScore <= 0 {
		cfg.NegativeActivationScore = 0.02
	}
	return cfg
}

func recallK(typ string, cfg Config) int {
	if typ == CaseMulti {
		return cfg.MultiRecallK
	}
	return cfg.SingleRecallK
}

func recallAt(returned, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(returned) {
		k = len(returned)
	}
	set := make(map[string]struct{}, k)
	for _, id := range returned[:k] {
		set[id] = struct{}{}
	}
	found := 0
	for _, id := range relevant {
		if _, ok := set[id]; ok {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

func activated(hits []search.Hit, threshold float64) bool {
	for _, hit := range hits {
		if hit.Score >= threshold {
			return true
		}
	}
	return false
}

func finiteVector(vector []float32) bool {
	for _, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return false
		}
	}
	return true
}

func vectorsEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validCaseType(typ string) bool {
	return typ == CaseSingle || typ == CaseParaphrase || typ == CaseNegative || typ == CaseMulti
}

func validateRatio(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("threshold %s must be between 0 and 1", name)
	}
	return nil
}

func cloneMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

type syntheticEmbedder struct {
	vectors map[string][]float32
}

func (e syntheticEmbedder) Embed(query string) ([]float32, error) {
	vector, ok := e.vectors[query]
	if !ok {
		return nil, fmt.Errorf("query %q has no synthetic vector", query)
	}
	return append([]float32(nil), vector...), nil
}

// JSON returns deterministic, indented report bytes. Cases retain fixture
// order, and no map is used in the serialized report.
func (r Report) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// SortedIDs is useful to callers displaying expected sets without changing
// ranked result order.
func SortedIDs(ids []string) []string {
	result := append([]string(nil), ids...)
	sort.Strings(result)
	return result
}
