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
