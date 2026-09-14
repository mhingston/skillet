package httpserver

import (
	"context"

	authz "github.com/mhingston/skillet/internal/authorization"
	"github.com/mhingston/skillet/internal/catalogue"
)

func (s *Server) capabilityAuthorizationResource(organizationID string, info catalogue.RevisionInfo) authz.Resource {
	resource := authz.Resource{OrganizationID: organizationID, ID: info.SkillID}
	service := configuredCapabilityServiceFor(s)
	if service == nil {
		return resource
	}
	if scope, ok := service.ScopeForRevision(info.RevisionID); ok {
		resource.OrganizationID = scope.Organization
		resource.Namespace = scope.Namespace
		resource.Repository = scope.Repository
		return resource
	}
	if scope, ok := service.ScopeForRepository(organizationID, info.RepositoryID); ok {
		resource.OrganizationID = scope.Organization
		resource.Namespace = scope.Namespace
		resource.Repository = scope.Repository
	}
	return resource
}

func (s *Server) authorizeCapabilityMaterialization(ctx context.Context, organizationID string, info catalogue.RevisionInfo) error {
	return s.authorize(ctx, authz.ActionCapabilityMaterialize, s.capabilityAuthorizationResource(organizationID, info))
}

func (s *Server) authorizeCapabilityDescription(ctx context.Context, organizationID string, info catalogue.RevisionInfo) error {
	return s.authorize(ctx, authz.ActionCapabilityDescribe, s.capabilityAuthorizationResource(organizationID, info))
}

func (s *Server) authorizeEvidenceResource(ctx context.Context, action authz.Action, organizationID, revisionID, stableID string) error {
	resource := authz.Resource{OrganizationID: organizationID, ID: stableID}
	if revisionID != "" && s.catalogue != nil {
		if info, err := s.catalogue.Revision(ctx, organizationID, revisionID); err == nil {
			resource = s.capabilityAuthorizationResource(organizationID, info)
			resource.ID = revisionID
		} else if resource.ID == "" {
			resource.ID = revisionID
		}
	}
	return s.authorize(ctx, action, resource)
}
