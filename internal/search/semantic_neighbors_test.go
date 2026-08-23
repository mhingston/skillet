package search

import "testing"

func TestSearchAddsDerivedSemanticNeighborsWithoutChangingRanking(t *testing.T) {
	idx, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	docs := []Document{
		{ID: "rev-plan", SkillID: "org/repo/plan", OrganizationID: "org", Name: "plan", Description: "planning implementation work", Vector: []float32{1, 0}, Searchable: true},
		{ID: "rev-review", SkillID: "org/repo/review", OrganizationID: "org", Name: "review", Description: "review implementation changes", Vector: []float32{0.9, 0.1}, Searchable: true},
		{ID: "rev-memory", SkillID: "org/repo/memory", OrganizationID: "org", Name: "memory", Description: "capture durable context", Vector: []float32{0, 1}, Searchable: true},
		{ID: "rev-other-org", SkillID: "other/repo/plan", OrganizationID: "other", Name: "other-plan", Description: "planning implementation work", Vector: []float32{1, 0}, Searchable: true},
	}
	for _, doc := range docs {
		if err := idx.Add(doc); err != nil {
			t.Fatal(err)
		}
	}

	hits, degraded, err := idx.SearchWithFilters("planning", 50, 50, 1, 60, Filters{OrganizationID: "org"})
	if err != nil {
		t.Fatal(err)
	}
	if !degraded {
		t.Fatal("search without a query embedder should still report embedding degradation")
	}
	if len(hits) != 1 || hits[0].ID != "rev-plan" || hits[0].Rank != 1 {
		t.Fatalf("unexpected ranked hit: %+v", hits)
	}
	if len(hits[0].SemanticNeighbors) != 1 {
		t.Fatalf("semantic neighbours = %+v, want exactly one positive same-organisation neighbour", hits[0].SemanticNeighbors)
	}
	neighbor := hits[0].SemanticNeighbors[0]
	if neighbor.Skill.SkillID != "org/repo/review" {
		t.Fatalf("nearest skill = %q, want org/repo/review", neighbor.Skill.SkillID)
	}
	if neighbor.Evidence != "routing_embedding_cosine" {
		t.Fatalf("evidence = %q", neighbor.Evidence)
	}
	if neighbor.Similarity <= 0.99 {
		t.Fatalf("similarity = %f, want > 0.99", neighbor.Similarity)
	}
}

func TestSemanticNeighborsAreBoundedAndDeterministic(t *testing.T) {
	idx, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "source", SkillID: "org/repo/source", OrganizationID: "org", Name: "source", Description: "source capability", Vector: []float32{1, 0}, Searchable: true}); err != nil {
		t.Fatal(err)
	}
	for _, doc := range []Document{
		{ID: "b", SkillID: "org/repo/b", OrganizationID: "org", Name: "b", Description: "related b", Vector: []float32{1, 0}, Searchable: true},
		{ID: "a", SkillID: "org/repo/a", OrganizationID: "org", Name: "a", Description: "related a", Vector: []float32{1, 0}, Searchable: true},
		{ID: "c", SkillID: "org/repo/c", OrganizationID: "org", Name: "c", Description: "related c", Vector: []float32{1, 0}, Searchable: true},
		{ID: "d", SkillID: "org/repo/d", OrganizationID: "org", Name: "d", Description: "related d", Vector: []float32{1, 0}, Searchable: true},
	} {
		if err := idx.Add(doc); err != nil {
			t.Fatal(err)
		}
	}

	hits, _, err := idx.SearchWithFilters("source", 50, 50, 1, 60, Filters{OrganizationID: "org"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits = %+v", hits)
	}
	neighbors := hits[0].SemanticNeighbors
	if len(neighbors) != semanticNeighborLimit {
		t.Fatalf("neighbour count = %d, want %d", len(neighbors), semanticNeighborLimit)
	}
	want := []string{"org/repo/a", "org/repo/b", "org/repo/c"}
	for i, skillID := range want {
		if neighbors[i].Skill.SkillID != skillID {
			t.Fatalf("neighbor[%d] = %q, want %q", i, neighbors[i].Skill.SkillID, skillID)
		}
	}
}

func TestSemanticNeighborsAreOmittedWithoutStoredVectors(t *testing.T) {
	idx, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "plan", SkillID: "org/repo/plan", OrganizationID: "org", Name: "plan", Description: "planning work", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	if err := idx.Add(Document{ID: "review", SkillID: "org/repo/review", OrganizationID: "org", Name: "review", Description: "review work", Searchable: true}); err != nil {
		t.Fatal(err)
	}
	hits, _, err := idx.SearchWithFilters("planning", 50, 50, 1, 60, Filters{OrganizationID: "org"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || len(hits[0].SemanticNeighbors) != 0 {
		t.Fatalf("unexpected semantic neighbours without vectors: %+v", hits)
	}
}
