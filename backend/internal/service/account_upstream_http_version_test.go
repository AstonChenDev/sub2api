package service

import (
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestAccountUpstreamHTTPVersion(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    UpstreamHTTPVersion
	}{
		{name: "nil account", account: nil, want: UpstreamHTTPVersionAuto},
		{name: "missing setting", account: &Account{Extra: map[string]any{}}, want: UpstreamHTTPVersionAuto},
		{name: "http1", account: &Account{Extra: map[string]any{UpstreamHTTPVersionExtraKey: "http1"}}, want: UpstreamHTTPVersionHTTP1},
		{name: "normalized http2", account: &Account{Extra: map[string]any{UpstreamHTTPVersionExtraKey: " HTTP2 "}}, want: UpstreamHTTPVersionHTTP2},
		{name: "invalid historical value", account: &Account{Extra: map[string]any{UpstreamHTTPVersionExtraKey: "h3"}}, want: UpstreamHTTPVersionAuto},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.UpstreamHTTPVersion())
		})
	}
}

func TestNormalizeUpstreamHTTPVersionExtra(t *testing.T) {
	t.Run("accepts supported account values", func(t *testing.T) {
		extra, err := normalizeUpstreamHTTPVersionExtra(PlatformOpenAI, map[string]any{
			UpstreamHTTPVersionExtraKey: " HTTP1 ",
			"preserved":                 true,
		})
		require.NoError(t, err)
		require.Equal(t, "http1", extra[UpstreamHTTPVersionExtraKey])
		require.Equal(t, true, extra["preserved"])
	})

	t.Run("accepts CN OpenAI-compatible platforms", func(t *testing.T) {
		for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
			extra, err := normalizeUpstreamHTTPVersionExtra(platform, map[string]any{UpstreamHTTPVersionExtraKey: "http2"})
			require.NoError(t, err)
			require.Equal(t, "http2", extra[UpstreamHTTPVersionExtraKey])
		}
	})

	t.Run("rejects malformed value", func(t *testing.T) {
		_, err := normalizeUpstreamHTTPVersionExtra(PlatformOpenAI, map[string]any{UpstreamHTTPVersionExtraKey: "h3"})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	})

	t.Run("rejects unsupported platform", func(t *testing.T) {
		_, err := normalizeUpstreamHTTPVersionExtra(PlatformAnthropic, map[string]any{UpstreamHTTPVersionExtraKey: "http1"})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	})

	t.Run("does not mutate caller map", func(t *testing.T) {
		input := map[string]any{UpstreamHTTPVersionExtraKey: " HTTP2 "}
		_, err := normalizeUpstreamHTTPVersionExtra(PlatformOpenAI, input)
		require.NoError(t, err)
		require.Equal(t, " HTTP2 ", input[UpstreamHTTPVersionExtraKey])
	})
}
