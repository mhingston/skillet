package skillspec

import (
	"testing"

	"github.com/mhingston/skillet/internal/composition"
)

func TestValidateCompositionMetadata(t *testing.T) {
	fm := Frontmatter{
		Name: "review-skill", Description: "review skill",
		Metadata: map[string]string{
			composition.MetadataRequires: `[{"id":"demo/repo/base","version":"^1.0.0"}]`,
			composition.MetadataConflicts: `[{"id":"demo/repo/legacy"}]`,
		},
	}
	for _, finding := range Validate(fm) {
		if finding.Code == FindingInvalidComposition { t.Fatalf("unexpected finding: %+v", finding) }
	}
}

func TestValidateRejectsMalformedCompositionMetadata(t *testing.T) {
	fm := Frontmatter{
		Name: "review-skill", Description: "review skill",
		Metadata: map[string]string{composition.MetadataRequires: `[{"id":"demo/repo/base","version":"not semver"}]`},
	}
	for _, finding := range Validate(fm) {
		if finding.Code == FindingInvalidComposition { return }
	}
	t.Fatal("expected invalid composition finding")
}
