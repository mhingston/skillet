package capability

import (
	"bytes"
	"os"
	"testing"

	"github.com/mhingston/skillet/internal/search"
	"gopkg.in/yaml.v3"
)

type scopedEvalSuite struct {
	Version int    `yaml:"version"`
	Name    string `yaml:"name"`
	Documents []struct {
		RevisionID       string `yaml:"revision_id"`
		SkillID          string `yaml:"skill_id"`
		SourceRepository string `yaml:"source_repository"`
		Name             string `yaml:"name"`
		Description      string `yaml:"description"`
		Scope            struct {
			Namespace  string `yaml:"namespace"`
			Repository string `yaml:"repository"`
		} `yaml:"scope"`
	} `yaml:"documents"`
	Cases []struct {
		ID           string   `yaml:"id"`
		Type         string   `yaml:"type"`
		Query        string   `yaml:"query"`
		RelevantIDs  []string `yaml:"relevant_ids"`
		ForbiddenIDs []string `yaml:"forbidden_ids"`
		Scope        struct {
			Namespace  string `yaml:"namespace"`
			Repository string `yaml:"repository"`
		} `yaml:"scope"`
	} `yaml:"cases"`
	Thresholds struct {
		Top1                        float64 `yaml:"top1"`
		RecallAt3                   float64 `yaml:"recall_at_3"`
		MultiRecallAt5              float64 `yaml:"multi_recall_at_5"`
		NegativeFalseActivationRate float64 `yaml:"negative_false_activation_rate"`
		ScopeLeakage                float64 `yaml:"scope_leakage"`
	} `yaml:"thresholds"`
}

func TestScopedCapabilityRoutingEval(t *testing.T) {
	raw, err := os.ReadFile("../../evals/capabilities.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var suite scopedEvalSuite
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&suite); err != nil {
		t.Fatal(err)
	}
	if suite.Version != 1 || suite.Name == "" || len(suite.Documents) == 0 || len(suite.Cases) == 0 {
		t.Fatalf("invalid capability eval fixture: %+v", suite)
	}

	idx, err := search.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var policies []SourcePolicy
	for _, fixture := range suite.Documents {
		if err := idx.Add(search.Document{
			ID: fixture.RevisionID, SkillID: fixture.SkillID, OrganizationID: "demo", RepositoryID: fixture.SourceRepository,
			Name: fixture.Name, Description: fixture.Description, TrustLevel: "approved", Searchable: true,
		}); err != nil {
			t.Fatal(err)
		}
		if fixture.Scope.Namespace != "" || fixture.Scope.Repository != "" {
			policies = append(policies, SourcePolicy{RepositoryID: fixture.SourceRepository, Scope: mustScope(t, "demo", fixture.Scope.Namespace, fixture.Scope.Repository)})
		}
	}
	service, err := New(idx, dedupePolicies(policies))
	if err != nil {
		t.Fatal(err)
	}

	var singleTop1, singleRecall, multiRecall, negativeActivation, leakage float64
	var singles, multis, negatives, leakageChecks int
	for _, c := range suite.Cases {
		scope := mustScope(t, "demo", c.Scope.Namespace, c.Scope.Repository)
		results, _, err := service.Search(c.Query, 50, 50, 5, 60, scope, search.Filters{})
		if err != nil {
			t.Fatalf("case %s: %v", c.ID, err)
		}
		returned := make([]string, 0, len(results))
		for _, result := range results {
			returned = append(returned, result.Capability.Provenance.RevisionID)
		}
		for _, forbidden := range c.ForbiddenIDs {
			leakageChecks++
			if containsID(returned, forbidden) {
				leakage++
			}
		}
		switch c.Type {
		case "single":
			singles++
			if len(returned) > 0 && len(c.RelevantIDs) == 1 && returned[0] == c.RelevantIDs[0] {
				singleTop1++
			}
			singleRecall += recallIDs(returned, c.RelevantIDs, 3)
		case "multi":
			multis++
			multiRecall += recallIDs(returned, c.RelevantIDs, 5)
		case "negative":
			negatives++
			if len(returned) > 0 {
				negativeActivation++
			}
		default:
			t.Fatalf("case %s has unsupported type %q", c.ID, c.Type)
		}
	}

	top1 := ratio(singleTop1, singles)
	recall3 := ratio(singleRecall, singles)
	multi5 := ratio(multiRecall, multis)
	negativeRate := ratio(negativeActivation, negatives)
	leakageRate := ratio(leakage, leakageChecks)
	if top1 < suite.Thresholds.Top1 || recall3 < suite.Thresholds.RecallAt3 || multi5 < suite.Thresholds.MultiRecallAt5 || negativeRate > suite.Thresholds.NegativeFalseActivationRate || leakageRate > suite.Thresholds.ScopeLeakage {
		t.Fatalf("scoped capability metrics top1=%.4f recall@3=%.4f multi_recall@5=%.4f negative_false_activation=%.4f scope_leakage=%.4f thresholds=%+v", top1, recall3, multi5, negativeRate, leakageRate, suite.Thresholds)
	}
}

func dedupePolicies(in []SourcePolicy) []SourcePolicy {
	seen := map[string]bool{}
	out := make([]SourcePolicy, 0, len(in))
	for _, policy := range in {
		if seen[policy.RepositoryID] {
			continue
		}
		seen[policy.RepositoryID] = true
		out = append(out, policy)
	}
	return out
}

func recallIDs(returned, relevant []string, k int) float64 {
	if len(relevant) == 0 {
		return 0
	}
	if k > len(returned) {
		k = len(returned)
	}
	found := 0
	for _, want := range relevant {
		if containsID(returned[:k], want) {
			found++
		}
	}
	return float64(found) / float64(len(relevant))
}

func containsID(ids []string, target string) bool {
	for _, id := range ids {
		if id == target {
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
