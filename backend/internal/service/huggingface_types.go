package service

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	PlatformHuggingFace   = domain.PlatformHuggingFace
	HFDefaultBaseURL      = "https://router.huggingface.co"
	HFMaxCredentialImport = 100_000

	HFDisabledReasonMonthlyExhausted = "monthly_included_credits_exhausted"
	HFDisabledReasonInvalidToken     = "invalid_token"
	HFDisabledReasonForbidden        = "forbidden"
	HFDisabledReasonDecryptFailed    = "credential_decrypt_failed"
	HFTemporaryReasonRateLimited     = "rate_limited"
	HFTemporaryReasonBillingRequired = "billing_required"
	HFTemporaryReasonTransient       = "transient_upstream_failure"
)

var (
	ErrHFFeatureDisabled     = infraerrors.BadRequest("HF_FEATURE_DISABLED", "hugging face key pools are disabled")
	ErrHFPoolNotFound        = infraerrors.NotFound("HF_POOL_NOT_FOUND", "hugging face pool not found")
	ErrHFPoolHasNoCandidates = errors.New("hugging face pool has no available credentials")
	ErrHFModelNotSupported   = errors.New("hugging face model is not supported by any configured pool")
	ErrHFPoolRateLimited     = errors.New("all matching hugging face pools are temporarily unavailable (rate_limited=1)")
	ErrHFImportTooLarge      = infraerrors.BadRequest("HF_IMPORT_TOO_LARGE", "a hugging face credential import cannot exceed 100000 entries")
)

// HuggingFacePool is the first-class upstream pool selected before individual
// credentials. Lower Priority values win; Weight is used only between pools at
// the same priority.
type HuggingFacePool struct {
	ID                     int64     `json:"id"`
	GroupID                int64     `json:"group_id"`
	Name                   string    `json:"name"`
	BaseURL                string    `json:"base_url"`
	Priority               int       `json:"priority"`
	Weight                 int       `json:"weight"`
	Status                 string    `json:"status"`
	Models                 []string  `json:"models"`
	FailureThreshold       int       `json:"failure_threshold"`
	CircuitCooldownSeconds int       `json:"circuit_cooldown_seconds"`
	CredentialCount        int64     `json:"credential_count,omitempty"`
	AvailableCount         int64     `json:"available_count,omitempty"`
	CooldownCount          int64     `json:"cooldown_count,omitempty"`
	DisabledCount          int64     `json:"disabled_count,omitempty"`
	CreatedAt              time.Time `json:"created_at"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type HuggingFacePoolInput struct {
	GroupID                int64    `json:"group_id"`
	Name                   string   `json:"name"`
	BaseURL                string   `json:"base_url"`
	Priority               int      `json:"priority"`
	Weight                 int      `json:"weight"`
	Status                 string   `json:"status"`
	Models                 []string `json:"models"`
	FailureThreshold       int      `json:"failure_threshold"`
	CircuitCooldownSeconds int      `json:"circuit_cooldown_seconds"`
}

type HuggingFaceCredentialImport struct {
	Token       string `json:"token"`
	Priority    int    `json:"priority"`
	Concurrency int    `json:"concurrency"`
}

type HFProtectedCredential struct {
	Fingerprint string
	Ciphertext  string
	Suffix      string
	Priority    int
	Concurrency int
}

type HFImportResult struct {
	Received     int  `json:"received"`
	Imported     int  `json:"imported"`
	Duplicate    int  `json:"duplicate"`
	Invalid      int  `json:"invalid"`
	IndexPending bool `json:"index_pending"`
}

type HFCredentialRef struct {
	AccountID int64
	PoolID    int64
	Priority  int
	ReadyAt   *time.Time
}

type HFCredentialListItem struct {
	AccountID              int64      `json:"account_id"`
	PoolID                 int64      `json:"pool_id"`
	Name                   string     `json:"name"`
	TokenSuffix            string     `json:"token_suffix"`
	Priority               int        `json:"priority"`
	Concurrency            int        `json:"concurrency"`
	Status                 string     `json:"status"`
	Schedulable            bool       `json:"schedulable"`
	DisabledReason         string     `json:"disabled_reason,omitempty"`
	UpstreamStatusCode     *int       `json:"upstream_status_code,omitempty"`
	ErrorMessage           string     `json:"error_message,omitempty"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at,omitempty"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until,omitempty"`
	RecoverAt              *time.Time `json:"recover_at,omitempty"`
	LastUsedAt             *time.Time `json:"last_used_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
}

type HFCredentialPage struct {
	Items  []HFCredentialListItem `json:"items"`
	Total  int64                  `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
}

type HFCredentialTransition struct {
	Status             string
	Schedulable        bool
	Reason             string
	ErrorMessage       string
	UpstreamStatusCode *int
	ReadyAt            *time.Time
	RecoverAt          *time.Time
	ClearTemporary     bool
}

type HFCredentialProtector interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
	Fingerprint(plaintext string) (string, error)
}

type HuggingFaceRepository interface {
	CreatePool(ctx context.Context, pool *HuggingFacePool) error
	UpdatePool(ctx context.Context, pool *HuggingFacePool) error
	GetPool(ctx context.Context, id int64) (*HuggingFacePool, error)
	ListPoolsByGroup(ctx context.Context, groupID int64, withStats bool) ([]HuggingFacePool, error)
	ListActivePools(ctx context.Context) ([]HuggingFacePool, error)
	DeletePool(ctx context.Context, id int64) error

	ImportCredentials(ctx context.Context, poolID int64, credentials []HFProtectedCredential) ([]HFCredentialRef, int, error)
	ListCredentialRefs(ctx context.Context, poolID int64) ([]HFCredentialRef, error)
	ListCredentials(ctx context.Context, poolID int64, limit, offset int) (*HFCredentialPage, error)
	TransitionCredential(ctx context.Context, accountID int64, fingerprint string, transition HFCredentialTransition) (HFCredentialRef, bool, error)
	RecoverDueCredentials(ctx context.Context, now time.Time, limit int) ([]HFCredentialRef, error)
	RecoverCredential(ctx context.Context, poolID, accountID int64) (HFCredentialRef, error)
	DeleteCredential(ctx context.Context, poolID, accountID int64) (HFCredentialRef, error)
}

// HuggingFaceCache owns only HF-prefixed keys. Its bounded ZSET rotation keeps
// per-request work independent from the total number of credentials.
type HuggingFaceCache interface {
	HasPoolIndex(ctx context.Context, poolID int64) (bool, error)
	// RebuildPoolIndex holds the same per-pool mutation lock used by Add,
	// Remove and Cooldown while loading the durable snapshot and publishing a
	// new generation. This prevents a concurrent failure transition from being
	// overwritten by a stale 100k-key rebuild.
	RebuildPoolIndex(ctx context.Context, poolID int64, loader func(context.Context) ([]HFCredentialRef, error)) error
	ListPriorities(ctx context.Context, poolID int64, limit int) ([]int, error)
	RotateCandidates(ctx context.Context, poolID int64, priority, limit int, now time.Time) ([]int64, error)
	AddCredential(ctx context.Context, ref HFCredentialRef) error
	RemoveCredential(ctx context.Context, ref HFCredentialRef) error
	CooldownCredential(ctx context.Context, ref HFCredentialRef, readyAt time.Time) error
	PickWeightedPool(ctx context.Context, groupID int64, pools []HuggingFacePool) (int64, error)
	PoolCooldownRemaining(ctx context.Context, poolID int64) (time.Duration, error)
	RecordPoolFailure(ctx context.Context, pool HuggingFacePool) error
	ClearPoolFailure(ctx context.Context, poolID int64) error
}

// HFFailoverReason is intentionally distinct from existing OpenAI/Grok reasons
// so handler error mapping cannot expose an upstream token or raw HF error.
const HFFailoverReason GatewayFailureReason = "hugging_face_credential_unavailable"

func newHFFailoverError(status int, retryAfter time.Duration, message string) *UpstreamFailoverError {
	headers := make(http.Header)
	if retryAfter > 0 {
		headers.Set("Retry-After", formatRetryAfterSeconds(retryAfter))
	}
	clientStatus := http.StatusServiceUnavailable
	if status == http.StatusTooManyRequests {
		clientStatus = http.StatusTooManyRequests
	}
	return &UpstreamFailoverError{
		StatusCode:        status,
		ResponseHeaders:   headers,
		Stage:             GatewayFailureStageAccountAuth,
		Scope:             GatewayFailureScopeAccount,
		Reason:            HFFailoverReason,
		NextAccountAction: NextAccountRetry,
		ClientStatusCode:  clientStatus,
		ClientMessage:     message,
	}
}

func formatRetryAfterSeconds(d time.Duration) string {
	seconds := int64(d.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}
