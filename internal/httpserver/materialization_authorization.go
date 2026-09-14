package httpserver

import (
	"context"
	"fmt"
	"net/url"

	"github.com/mhingston/skillet/internal/catalogue"
	"github.com/mhingston/skillet/internal/packageurl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// materializeAuthorizedRevision delegates to the existing immutable
// materialisation path only after authorization has succeeded, then strengthens
// claims-mode package URLs by binding their signed token to the exact revision
// that was authorized. Compatibility mode never calls this wrapper.
func (s *Server) materializeAuthorizedRevision(ctx context.Context, req *mcp.CallToolRequest, input materializeInput, info catalogue.RevisionInfo) (*mcp.CallToolResult, materializeOutput, error) {
	result, out, err := s.materializeTool(ctx, req, input)
	if err != nil {
		return result, out, err
	}
	organizationID := s.organizationID
	if authenticated, ok := OrganizationID(ctx); ok {
		organizationID = authenticated
	}
	if err := s.bindMaterializationRevision(organizationID, info, result, &out); err != nil {
		return nil, materializeOutput{}, err
	}
	return result, out, nil
}

func (s *Server) bindMaterializationRevision(organizationID string, info catalogue.RevisionInfo, result *mcp.CallToolResult, out *materializeOutput) error {
	if out == nil || info.RevisionID == "" {
		return fmt.Errorf("materialization revision is required")
	}
	replacements := map[string]string{}
	rebind := func(pkg *materializePackage) error {
		if pkg == nil || pkg.DownloadURL == "" || pkg.ArchiveSHA256 == "" || pkg.Format == "" || pkg.ExpiresAt.IsZero() {
			return fmt.Errorf("materialization package identity is incomplete")
		}
		token, err := s.packageSigner.Sign(packageurl.Payload{
			Version:        1,
			OrganizationID: organizationID,
			RevisionID:     info.RevisionID,
			Digest:         pkg.ArchiveSHA256,
			Format:         pkg.Format,
			ExpiresAt:      pkg.ExpiresAt.Unix(),
		})
		if err != nil {
			return err
		}
		oldURL := pkg.DownloadURL
		newURL, err := packageURLWithToken(oldURL, token)
		if err != nil {
			return err
		}
		replacements[oldURL] = newURL
		pkg.DownloadURL = newURL
		pkg.ResourceURI = newURL
		return nil
	}

	if err := rebind(&out.Package); err != nil {
		return err
	}
	out.Materialization.Command = materializationCommandFor(out.Materialization.Shell, out.Package.DownloadURL, out.Package.ArchiveSHA256, out.Destination.Directory, info)

	for i := range out.Variants {
		variant := &out.Variants[i]
		if err := rebind(&variant.Package); err != nil {
			return err
		}
		variant.Command = materializationCommandFor(variant.Shell, variant.Package.DownloadURL, variant.Package.ArchiveSHA256, variant.Destination, info)
	}

	if result != nil {
		for _, content := range result.Content {
			link, ok := content.(*mcp.ResourceLink)
			if !ok {
				continue
			}
			if replacement, ok := replacements[link.URI]; ok {
				link.URI = replacement
			}
		}
	}
	return nil
}

func materializationCommandFor(shell, download, digest, destination string, info catalogue.RevisionInfo) string {
	if shell == "powershell" {
		return powershellCommand(download, digest, destination, info.Name, info.SkillID, info.Commit)
	}
	return posixCommand(download, digest, destination, info.Name, info.SkillID, info.Commit)
}

func packageURLWithToken(raw, token string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid package URL")
	}
	query := u.Query()
	query.Set("token", token)
	u.RawQuery = query.Encode()
	return u.String(), nil
}
