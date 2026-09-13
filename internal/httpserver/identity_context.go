package httpserver

import (
	"context"

	authn "github.com/mhingston/skillet/internal/auth"
)

type identityContextKey struct{}

func withAuthenticatedIdentity(ctx context.Context, identity authn.Identity) context.Context {
	ctx = context.WithValue(ctx, identityContextKey{}, identity)
	return context.WithValue(ctx, organizationContextKey{}, identity.OrganizationID)
}

// Identity returns the trusted identity produced by the configured
// authentication validator. Development-mode requests do not synthesize one.
func Identity(ctx context.Context) (authn.Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(authn.Identity)
	return identity, ok && identity.Subject != "" && identity.OrganizationID != ""
}
