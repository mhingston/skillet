package main

import (
	"fmt"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/config"
	"github.com/mhingston/skillet/internal/search"
)

func configuredCapabilityService(index *search.Index, c config.Config) (*capability.Service, error) {
	policies := make([]capability.SourcePolicy, 0, len(c.Repositories))
	for i, repository := range c.Repositories {
		configured := repository.CapabilityScope
		if configured.Namespace == "" && configured.Repository == "" {
			continue
		}
		scope, err := capability.NewScope(c.Organization.ID, configured.Namespace, configured.Repository)
		if err != nil {
			return nil, fmt.Errorf("repositories[%d].capability_scope: %w", i, err)
		}
		policies = append(policies, capability.SourcePolicy{RepositoryID: repository.ID, Scope: scope})
	}
	return capability.New(index, policies)
}

// legacyRoutingDocuments returns only organisation-wide sources. Repository or
// namespace-scoped capabilities are deliberately absent from the v1
// search_skills index, which has no scope input and therefore cannot safely
// expose local-only content.
func legacyRoutingDocuments(docs []search.Document, repositories []config.Repository) []search.Document {
	scoped := make(map[string]struct{})
	for _, repository := range repositories {
		if repository.CapabilityScope.Namespace != "" || repository.CapabilityScope.Repository != "" {
			scoped[repository.ID] = struct{}{}
		}
	}
	if len(scoped) == 0 {
		return docs
	}
	out := make([]search.Document, 0, len(docs))
	for _, doc := range docs {
		if _, local := scoped[doc.RepositoryID]; local {
			continue
		}
		out = append(out, doc)
	}
	return out
}
