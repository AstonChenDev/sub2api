package service

import (
	"context"
	"testing"
)

func TestWithHTTPUpstreamProfile_DefaultKeepsContext(t *testing.T) {
	ctx := context.Background()
	got := WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileDefault)
	if got != ctx {
		t.Fatal("default profile should not wrap context")
	}
}

func TestWithHTTPUpstreamProfile_OpenAI(t *testing.T) {
	ctx := WithHTTPUpstreamProfile(context.TODO(), HTTPUpstreamProfileOpenAI)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileOpenAI, profile)
	}
}

func TestWithHTTPUpstreamProfileForAccount(t *testing.T) {
	account := &Account{Extra: map[string]any{UpstreamHTTPVersionExtraKey: "http1"}}
	ctx := WithHTTPUpstreamProfileForAccount(context.Background(), HTTPUpstreamProfileOpenAI, account)
	if profile := HTTPUpstreamProfileFromContext(ctx); profile != HTTPUpstreamProfileOpenAI {
		t.Fatalf("expected profile %q, got %q", HTTPUpstreamProfileOpenAI, profile)
	}
	if version := UpstreamHTTPVersionFromContext(ctx); version != UpstreamHTTPVersionHTTP1 {
		t.Fatalf("expected version %q, got %q", UpstreamHTTPVersionHTTP1, version)
	}
}

func TestWithHTTPUpstreamProfileForAccountAutoDoesNotForceProtocol(t *testing.T) {
	ctx := WithHTTPUpstreamProfileForAccount(context.Background(), HTTPUpstreamProfileOpenAI, &Account{})
	if version := UpstreamHTTPVersionFromContext(ctx); version != UpstreamHTTPVersionAuto {
		t.Fatalf("expected auto version, got %q", version)
	}
}

func TestWithHTTPUpstreamRedirectsDisabled(t *testing.T) {
	//nolint:staticcheck // Exercises the defensive nil-context fallback.
	ctx := WithHTTPUpstreamRedirectsDisabled(nil)
	if !HTTPUpstreamRedirectsDisabled(ctx) {
		t.Fatal("expected redirects to be disabled")
	}
	if HTTPUpstreamRedirectsDisabled(context.Background()) {
		t.Fatal("redirects should remain enabled by default")
	}
}
