// Package knowledgeeval evaluates the Markdown knowledge retrieval domain using
// deterministic local fixtures and embeddings. It performs no network calls.
package knowledgeeval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mhingston/skillet/internal/knowledge"
	"gopkg.in/yaml.v3"
)

type Thresholds struct {
	Top1Accuracy      float64 `yaml:"top1_accuracy" json:"top1_accuracy"`
	RecallAt3         float64 `yaml:"recall_at3" json:"recall_at3"`
	MRR               float64 `yaml:"mrr" json:"mrr"`
	NegativePrecision float64 `yaml:"negative_precision" json:"negative_precision"`
	MaxRegression     float64 `yaml:"max_regression" json:"max_regression"`
}

type Case struct {
	ID           string `yaml:"id"`
	Query        string `yaml:"query"`
	ExpectedPath string `yaml:"expected_path,omitempty"`
	Negative     bool   `yaml:"negative,omitempty"`
}

type Suite struct {
	Name                  string     `yaml:"name"`
	Version               int        `yaml:"version"`
	Corpus                string     `yaml:"corpus"`
	SourceID              string     `yaml:"source_id"`
	SourceLocator         string     `yaml:"source_locator"`
	SourceRevision        string     `yaml:"source_revision"`
	DeleteAfterFirstIndex []string   `yaml:"delete_after_first_index"`
	Thresholds            Thresholds `yaml:"thresholds"`
	Cases                 []Case     `yaml:"cases"`
}

type Metrics struct {
	Top1Accuracy      float64 `json:"top1_accuracy"`
	RecallAt3         float64 `json:"recall_at3"`
	MRR               float64 `json:"mrr"`
	NegativePrecision float64 `json:"negative_precision"`
}

type CaseResult struct {
	ID           string `json:"id"`
	Negative     bool   `json:"negative"`
	ExpectedPath string `json:"expected_path,omitempty"`
	TopPath      string `json:"top_path,omitempty"`
	FoundRank    int    `json:"found_rank,omitempty"`
	ResultCount  int    `json:"result_count"`
	Passed       bool   `json:"passed"`
}

type Report struct {
	SchemaVersion  int         `json:"schema_version"`
	Suite          string      `json:"suite"`
	FixtureVersion int         `json:"fixture_version"`
	Passed         bool        `json:"passed"`
	Metrics        Metrics     `json:"metrics"`
	Thresholds     Thresholds  `json:"thresholds"`
	Cases          []CaseResult `json:"cases"`
}

func Load(path string) (Suite, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("read knowledge eval fixture: %w", err)
	}
	var suite Suite
	if err := yaml.Unmarshal(contents, &suite); err != nil {
		return Suite{}, fmt.Errorf("decode knowledge eval fixture: %w", err)
	}
	if strings.TrimSpace(suite.Name) == "" || suite.Version < 1 || strings.TrimSpace(suite.Corpus) == "" || strings.TrimSpace(suite.SourceID) == "" {
		return Suite{}, errors.New("knowledge eval fixture requires name, positive version, corpus, and source_id")
	}
	if len(suite.Cases) == 0 {
		return Suite{}, errors.New("knowledge eval fixture requires cases")
	}
	for _, evalCase := range suite.Cases {
		if evalCase.ID == "" || evalCase.Query == "" || (!evalCase.Negative && evalCase.ExpectedPath == "") {
			return Suite{}, fmt.Errorf("invalid knowledge eval case %q", evalCase.ID)
		}
	}
	return suite, nil
}

func EvaluateFile(ctx context.Context, fixturePath, baselinePath string) (Report, error) {
	suite, err := Load(fixturePath)
	if err != nil {
		return Report{}, err
	}
	corpusPath := suite.Corpus
	if !filepath.IsAbs(corpusPath) {
		corpusPath = filepath.Join(filepath.Dir(fixturePath), corpusPath)
	}
	working := filepath.Join(os.TempDir(), "skillet-knowledge-eval-"+suite.Name)
	if err := os.RemoveAll(working); err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(working)
	if err := copyTree(corpusPath, working); err != nil {
		return Report{}, fmt.Errorf("copy knowledge eval corpus: %w", err)
	}

	service, err := knowledge.Open(ctx, filepath.Join(working, ".data"), knowledge.Options{Embedder: semanticEmbedder{}})
	if err != nil {
		return Report{}, err
	}
	defer service.Close()
	source := knowledge.Source{ID: suite.SourceID, Root: working, Locator: suite.SourceLocator, Revision: suite.SourceRevision}
	if _, err := service.Reindex(ctx, []knowledge.Source{source}); err != nil {
		return Report{}, fmt.Errorf("initial knowledge eval index: %w", err)
	}
	for _, relative := range suite.DeleteAfterFirstIndex {
		target, err := safeFixturePath(working, relative)
		if err != nil {
			return Report{}, err
		}
		if err := os.Remove(target); err != nil {
			return Report{}, fmt.Errorf("delete knowledge eval stale fixture %q: %w", relative, err)
		}
	}
	if len(suite.DeleteAfterFirstIndex) > 0 {
		source.Revision += "-after-delete"
		if _, err := service.Reindex(ctx, []knowledge.Source{source}); err != nil {
			return Report{}, fmt.Errorf("post-delete knowledge eval index: %w", err)
		}
	}

	report := Report{SchemaVersion: 1, Suite: suite.Name, FixtureVersion: suite.Version, Passed: true, Thresholds: suite.Thresholds}
	positive, top1, recall3, reciprocal := 0, 0, 0, 0.0
	negative, negativeCorrect := 0, 0
	for _, evalCase := range suite.Cases {
		response, err := service.Search(ctx, evalCase.Query, 3)
		if err != nil {
			return Report{}, fmt.Errorf("knowledge eval case %q: %w", evalCase.ID, err)
		}
		result := CaseResult{ID: evalCase.ID, Negative: evalCase.Negative, ExpectedPath: evalCase.ExpectedPath, ResultCount: len(response.Results)}
		if len(response.Results) > 0 {
			result.TopPath = response.Results[0].Path
		}
		if evalCase.Negative {
			negative++
			result.Passed = len(response.Results) == 0
			if result.Passed {
				negativeCorrect++
			}
		} else {
			positive++
			for rank, candidate := range response.Results {
				if candidate.Path == filepath.ToSlash(evalCase.ExpectedPath) {
					result.FoundRank = rank + 1
					break
				}
			}
			result.Passed = result.FoundRank > 0 && result.FoundRank <= 3
			if result.FoundRank == 1 {
				top1++
			}
			if result.FoundRank > 0 && result.FoundRank <= 3 {
				recall3++
				reciprocal += 1.0 / float64(result.FoundRank)
			}
		}
		report.Cases = append(report.Cases, result)
	}
	if positive > 0 {
		report.Metrics.Top1Accuracy = float64(top1) / float64(positive)
		report.Metrics.RecallAt3 = float64(recall3) / float64(positive)
		report.Metrics.MRR = reciprocal / float64(positive)
	}
	if negative > 0 {
		report.Metrics.NegativePrecision = float64(negativeCorrect) / float64(negative)
	}
	report.Passed = passesThresholds(report.Metrics, suite.Thresholds)

	if baselinePath != "" {
		baselineContents, err := os.ReadFile(baselinePath)
		if err != nil {
			return Report{}, fmt.Errorf("read knowledge eval baseline: %w", err)
		}
		var baseline Report
		if err := json.Unmarshal(baselineContents, &baseline); err != nil {
			return Report{}, fmt.Errorf("decode knowledge eval baseline: %w", err)
		}
		if baseline.Suite != report.Suite || baseline.FixtureVersion != report.FixtureVersion {
			return Report{}, errors.New("knowledge eval baseline does not match fixture suite/version")
		}
		maxRegression := suite.Thresholds.MaxRegression
		if report.Metrics.Top1Accuracy < baseline.Metrics.Top1Accuracy-maxRegression ||
			report.Metrics.RecallAt3 < baseline.Metrics.RecallAt3-maxRegression ||
			report.Metrics.MRR < baseline.Metrics.MRR-maxRegression ||
			report.Metrics.NegativePrecision < baseline.Metrics.NegativePrecision-maxRegression {
			report.Passed = false
		}
	}
	return report, nil
}

func passesThresholds(metrics Metrics, thresholds Thresholds) bool {
	return metrics.Top1Accuracy >= thresholds.Top1Accuracy &&
		metrics.RecallAt3 >= thresholds.RecallAt3 &&
		metrics.MRR >= thresholds.MRR &&
		metrics.NegativePrecision >= thresholds.NegativePrecision
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, 0o644)
	})
}

func safeFixturePath(root, relative string) (string, error) {
	clean := filepath.Clean(relative)
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid knowledge eval fixture path %q", relative)
	}
	return filepath.Join(root, clean), nil
}

type semanticEmbedder struct{}

func (semanticEmbedder) Embed(input string) ([]float32, error) {
	input = strings.ToLower(input)
	concepts := [][]string{
		{"roadside", "breakdown", "tow", "stranded", "motorist", "vehicle", "car"},
		{"retention", "renewal", "save", "churn", "offer"},
		{"incident", "outage", "sev", "rollback", "revert", "recovery"},
		{"slo", "service level", "availability", "latency", "reliability"},
		{"key performance", "kpi", "metric", "measure", "indicator", "dashboard"},
		{"access", "permission", "rbac", "role", "authorization"},
		{"legacy", "zebra", "escrow"},
	}
	vector := make([]float32, len(concepts))
	for dimension, terms := range concepts {
		for _, term := range terms {
			if strings.Contains(input, term) {
				vector[dimension]++
			}
		}
	}
	return vector, nil
}
