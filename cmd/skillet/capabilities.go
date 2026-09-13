package main

import (
	"fmt"

	"github.com/mhingston/skillet/internal/capability"
	"github.com/mhingston/skillet/internal/config"
	"github.com/mhingston/skillet/internal/governance"
	"github.com/mhingston/skillet/internal/mcptool"
	"github.com/mhingston/skillet/internal/search"
)

type configuredMCPToolCapabilities struct {
	Documents []search.Document
	Details   []capability.Detail
	Policies  []capability.SourcePolicy
}

func loadConfiguredMCPToolCapabilities(c config.Config) (configuredMCPToolCapabilities, error) {
	out := configuredMCPToolCapabilities{}
	identitySources := map[string]string{}
	revisionSources := map[string]string{}
	for i, configured := range c.MCPToolCatalogues {
		scope, err := capability.NewScope(c.Organization.ID, configured.CapabilityScope.Namespace, configured.CapabilityScope.Repository)
		if err != nil {
			return configuredMCPToolCapabilities{}, fmt.Errorf("mcp_tool_catalogues[%d].capability_scope: %w", i, err)
		}
		set, err := mcptool.LoadFile(configured.Path, mcptool.Options{
			CatalogueID: configured.ID,
			Scope:       scope,
			TrustLevel:  configured.TrustLevel,
		})
		if err != nil {
			return configuredMCPToolCapabilities{}, err
		}
		for _, detail := range set.Details {
			stableID := detail.Descriptor.Identity.ID
			if previous, exists := identitySources[stableID]; exists {
				return configuredMCPToolCapabilities{}, fmt.Errorf("duplicate MCP server/tool identity %q across catalogues %q and %q", stableID, previous, configured.ID)
			}
			identitySources[stableID] = configured.ID
			revisionID := detail.Descriptor.Provenance.RevisionID
			if previous, exists := revisionSources[revisionID]; exists {
				return configuredMCPToolCapabilities{}, fmt.Errorf("duplicate MCP tool revision %q across catalogues %q and %q", revisionID, previous, configured.ID)
			}
			revisionSources[revisionID] = configured.ID
		}
		out.Documents = append(out.Documents, set.Documents...)
		out.Details = append(out.Details, set.Details...)
		out.Policies = append(out.Policies, capability.SourcePolicy{RepositoryID: configured.ID, Scope: scope})
	}
	return out, nil
}

func configuredCapabilityService(index *search.Index, c config.Config, toolSets ...configuredMCPToolCapabilities) (*capability.Service, error) {
	policies := make([]capability.SourcePolicy, 0, len(c.Repositories)+len(c.MCPToolCatalogues))
	for i, repository := range c.Repositories {
		configured := repository.CapabilityScope
		scope, err := capability.NewScope(c.Organization.ID, configured.Namespace, configured.Repository)
		if err != nil {
			return nil, fmt.Errorf("repositories[%d].capability_scope: %w", i, err)
		}
		policies = append(policies, capability.SourcePolicy{
			RepositoryID: repository.ID,
			Scope:        scope,
			Owner:        repository.Owner,
		})
	}
	var details []capability.Detail
	for _, tools := range toolSets {
		policies = append(policies, tools.Policies...)
		details = append(details, tools.Details...)
	}
	service, err := capability.New(index, policies)
	if err != nil {
		return nil, err
	}
	if err := service.RegisterDetails(details); err != nil {
		return nil, err
	}
	if err := service.RefreshGovernance(); err != nil {
		return nil, err
	}
	return service, nil
}

func hasScopedCapabilitySources(repositories []config.Repository) bool {
	for _, repository := range repositories {
		if repository.CapabilityScope.Namespace != "" || repository.CapabilityScope.Repository != "" {
			return true
		}
	}
	return false
}

func capabilityRoutingDocuments(skills, tools []search.Document) []search.Document {
	out := make([]search.Document, 0, len(skills)+len(tools))
	out = append(out, skills...)
	out = append(out, tools...)
	return out
}

// capabilityMetadataKeys preserves publisher governance/control metadata even
// when semantic metadata is explicitly allow-listed. The search index itself
// strips these reserved keys from lexical and embedding routing text.
func capabilityMetadataKeys(configured []string) []string {
	if len(configured) == 0 {
		return nil
	}
	out := append([]string(nil), configured...)
	for _, key := range []string{
		governance.StateKey,
		governance.OwnerKey,
		governance.MaintainersKey,
		governance.ReasonKey,
		governance.DeprecatedKey,
		governance.ReplacedByKey,
	} {
		seen := false
		for _, current := range out {
			if current == key {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, key)
		}
	}
	return out
}

// legacyRoutingDocuments returns only organisation-wide skill sources that
// remain eligible for new work. Repository or namespace-scoped skills and all
// MCP tool catalogues are absent from the v1 search_skills index, which has no
// scope/kind input and must stay skill-only. Invalid or yanked governance fails
// closed on this compatibility surface; the capability service reports the
// validation error on the scoped surface.
func legacyRoutingDocuments(docs []search.Document, repositories []config.Repository) []search.Document {
	scoped := make(map[string]struct{})
	for _, repository := range repositories {
		if repository.CapabilityScope.Namespace != "" || repository.CapabilityScope.Repository != "" {
			scoped[repository.ID] = struct{}{}
		}
	}
	out := make([]search.Document, 0, len(docs))
	for _, doc := range docs {
		if _, local := scoped[doc.RepositoryID]; local {
			continue
		}
		state, _, err := governance.Parse(doc.Metadata, governance.Defaults{})
		if err != nil || state == governance.StateYanked {
			doc.Searchable = false
		}
		out = append(out, doc)
	}
	return out
}
