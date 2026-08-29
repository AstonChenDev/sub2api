package service

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// UpstreamHTTPVersion controls the HTTP protocol used by OpenAI-compatible
// upstream requests for one account. Auto inherits the gateway-wide policy.
type UpstreamHTTPVersion string

const (
	UpstreamHTTPVersionAuto  UpstreamHTTPVersion = "auto"
	UpstreamHTTPVersionHTTP1 UpstreamHTTPVersion = "http1"
	UpstreamHTTPVersionHTTP2 UpstreamHTTPVersion = "http2"

	// UpstreamHTTPVersionExtraKey is intentionally stored in accounts.extra so
	// the setting can be changed without a schema migration.
	UpstreamHTTPVersionExtraKey = "upstream_http_version"
)

// SupportsUpstreamHTTPVersion reports whether the account platform uses the
// configurable OpenAI-compatible HTTP transport. Grok has its own transport
// profile, while Hugging Face credentials are managed through dedicated pools.
func SupportsUpstreamHTTPVersion(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformOpenAI, PlatformKimi, PlatformZhipu, PlatformDeepseek:
		return true
	default:
		return false
	}
}

func normalizeUpstreamHTTPVersion(value any) (UpstreamHTTPVersion, error) {
	raw, ok := value.(string)
	if !ok {
		return "", infraerrors.New(http.StatusBadRequest, "UPSTREAM_HTTP_VERSION_INVALID",
			"upstream_http_version must be one of auto, http1, or http2")
	}
	version := UpstreamHTTPVersion(strings.ToLower(strings.TrimSpace(raw)))
	switch version {
	case UpstreamHTTPVersionAuto, UpstreamHTTPVersionHTTP1, UpstreamHTTPVersionHTTP2:
		return version, nil
	default:
		return "", infraerrors.New(http.StatusBadRequest, "UPSTREAM_HTTP_VERSION_INVALID",
			"upstream_http_version must be one of auto, http1, or http2")
	}
}

func parseUpstreamHTTPVersionExtra(extra map[string]any) (UpstreamHTTPVersion, bool, error) {
	if extra == nil {
		return UpstreamHTTPVersionAuto, false, nil
	}
	raw, ok := extra[UpstreamHTTPVersionExtraKey]
	if !ok {
		return UpstreamHTTPVersionAuto, false, nil
	}
	version, err := normalizeUpstreamHTTPVersion(raw)
	if err != nil {
		return "", true, err
	}
	return version, true, nil
}

func normalizeUpstreamHTTPVersionExtra(platform string, extra map[string]any) (map[string]any, error) {
	version, provided, err := parseUpstreamHTTPVersionExtra(extra)
	if err != nil || !provided {
		return extra, err
	}
	if !SupportsUpstreamHTTPVersion(platform) {
		return nil, infraerrors.New(http.StatusBadRequest, "UPSTREAM_HTTP_VERSION_UNSUPPORTED",
			"upstream_http_version is only supported for OpenAI-compatible account platforms")
	}
	normalized := make(map[string]any, len(extra))
	for key, value := range extra {
		normalized[key] = value
	}
	normalized[UpstreamHTTPVersionExtraKey] = string(version)
	return normalized, nil
}

// UpstreamHTTPVersion returns a validated effective account preference.
// Invalid historical data safely falls back to auto; all admin write paths
// reject invalid values before persistence.
func (a *Account) UpstreamHTTPVersion() UpstreamHTTPVersion {
	if a == nil || a.Extra == nil {
		return UpstreamHTTPVersionAuto
	}
	version, provided, err := parseUpstreamHTTPVersionExtra(a.Extra)
	if err != nil || !provided {
		return UpstreamHTTPVersionAuto
	}
	return version
}
