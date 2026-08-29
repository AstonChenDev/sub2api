package service

import "context"

// HTTPUpstreamProfile marks HTTP upstream requests that need provider-specific
// transport policy.
type HTTPUpstreamProfile string

const (
	HTTPUpstreamProfileDefault HTTPUpstreamProfile = ""
	HTTPUpstreamProfileOpenAI  HTTPUpstreamProfile = "openai"
	HTTPUpstreamProfileGrok    HTTPUpstreamProfile = "grok"
)

type httpUpstreamProfileContextKey struct{}
type httpUpstreamDisableRedirectsContextKey struct{}
type upstreamHTTPVersionContextKey struct{}

// WithHTTPUpstreamProfile injects an upstream transport profile into ctx.
func WithHTTPUpstreamProfile(ctx context.Context, profile HTTPUpstreamProfile) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if profile == HTTPUpstreamProfileDefault {
		return ctx
	}
	return context.WithValue(ctx, httpUpstreamProfileContextKey{}, profile)
}

// HTTPUpstreamProfileFromContext resolves the upstream transport profile from ctx.
func HTTPUpstreamProfileFromContext(ctx context.Context) HTTPUpstreamProfile {
	if ctx == nil {
		return HTTPUpstreamProfileDefault
	}
	profile, ok := ctx.Value(httpUpstreamProfileContextKey{}).(HTTPUpstreamProfile)
	if !ok {
		return HTTPUpstreamProfileDefault
	}
	switch profile {
	case HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileGrok:
		return profile
	default:
		return HTTPUpstreamProfileDefault
	}
}

// WithHTTPUpstreamProfileForAccount applies both the provider transport profile
// and the account-level HTTP version preference to one upstream request.
func WithHTTPUpstreamProfileForAccount(ctx context.Context, profile HTTPUpstreamProfile, account *Account) context.Context {
	ctx = WithHTTPUpstreamProfile(ctx, profile)
	if account == nil {
		return ctx
	}
	return WithUpstreamHTTPVersion(ctx, account.UpstreamHTTPVersion())
}

// WithUpstreamHTTPVersion injects an explicit account-level HTTP version.
// Auto is represented by the absence of a context value.
func WithUpstreamHTTPVersion(ctx context.Context, version UpstreamHTTPVersion) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if version != UpstreamHTTPVersionHTTP1 && version != UpstreamHTTPVersionHTTP2 {
		return ctx
	}
	return context.WithValue(ctx, upstreamHTTPVersionContextKey{}, version)
}

// UpstreamHTTPVersionFromContext resolves the account preference attached to
// an upstream request. Missing or invalid values inherit the global policy.
func UpstreamHTTPVersionFromContext(ctx context.Context) UpstreamHTTPVersion {
	if ctx == nil {
		return UpstreamHTTPVersionAuto
	}
	version, ok := ctx.Value(upstreamHTTPVersionContextKey{}).(UpstreamHTTPVersion)
	if !ok {
		return UpstreamHTTPVersionAuto
	}
	switch version {
	case UpstreamHTTPVersionHTTP1, UpstreamHTTPVersionHTTP2:
		return version
	default:
		return UpstreamHTTPVersionAuto
	}
}

// WithHTTPUpstreamRedirectsDisabled prevents credential-bearing probes from
// following redirects through the shared upstream client.
func WithHTTPUpstreamRedirectsDisabled(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, httpUpstreamDisableRedirectsContextKey{}, true)
}

func HTTPUpstreamRedirectsDisabled(ctx context.Context) bool {
	return ctx != nil && ctx.Value(httpUpstreamDisableRedirectsContextKey{}) == true
}
