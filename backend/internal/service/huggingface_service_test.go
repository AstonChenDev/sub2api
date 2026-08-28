package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hfTestRepo struct {
	HuggingFaceRepository
	pools      []HuggingFacePool
	transition HFCredentialTransition
	imported   []HFProtectedCredential
}

func (r *hfTestRepo) ListPoolsByGroup(context.Context, int64, bool) ([]HuggingFacePool, error) {
	return append([]HuggingFacePool(nil), r.pools...), nil
}

func (r *hfTestRepo) GetPool(_ context.Context, id int64) (*HuggingFacePool, error) {
	for _, pool := range r.pools {
		if pool.ID == id {
			copyPool := pool
			return &copyPool, nil
		}
	}
	return nil, ErrHFPoolNotFound
}

func (r *hfTestRepo) TransitionCredential(_ context.Context, accountID int64, _ string, transition HFCredentialTransition) (HFCredentialRef, bool, error) {
	r.transition = transition
	return HFCredentialRef{AccountID: accountID, PoolID: 10, Priority: 50, ReadyAt: transition.ReadyAt}, true, nil
}

func (r *hfTestRepo) ImportCredentials(_ context.Context, poolID int64, credentials []HFProtectedCredential) ([]HFCredentialRef, int, error) {
	r.imported = append([]HFProtectedCredential(nil), credentials...)
	refs := make([]HFCredentialRef, 0, len(credentials))
	for i, credential := range credentials {
		refs = append(refs, HFCredentialRef{AccountID: int64(i + 1), PoolID: poolID, Priority: credential.Priority})
	}
	return refs, 0, nil
}

func (r *hfTestRepo) ListCredentialRefs(context.Context, int64) ([]HFCredentialRef, error) {
	return nil, nil
}

type hfTestCache struct {
	HuggingFaceCache
	priorities    []int
	priorityLimit int
	rotateCalls   int
	rotateIDs     []int64
	removed       int
	cooled        int
	failures      int
	cleared       int
	rebuildErr    error
}

func (c *hfTestCache) HasPoolIndex(context.Context, int64) (bool, error) { return true, nil }
func (c *hfTestCache) ListPriorities(_ context.Context, _ int64, limit int) ([]int, error) {
	c.priorityLimit = limit
	if limit > len(c.priorities) {
		limit = len(c.priorities)
	}
	return c.priorities[:limit], nil
}
func (c *hfTestCache) RotateCandidates(context.Context, int64, int, int, time.Time) ([]int64, error) {
	c.rotateCalls++
	return append([]int64(nil), c.rotateIDs...), nil
}
func (c *hfTestCache) PoolCooldownRemaining(context.Context, int64) (time.Duration, error) {
	return 0, nil
}
func (c *hfTestCache) PickWeightedPool(_ context.Context, _ int64, pools []HuggingFacePool) (int64, error) {
	if len(pools) == 0 {
		return 0, nil
	}
	return pools[0].ID, nil
}
func (c *hfTestCache) RemoveCredential(context.Context, HFCredentialRef) error {
	c.removed++
	return nil
}
func (c *hfTestCache) CooldownCredential(context.Context, HFCredentialRef, time.Time) error {
	c.cooled++
	return nil
}
func (c *hfTestCache) RecordPoolFailure(context.Context, HuggingFacePool) error {
	c.failures++
	return nil
}
func (c *hfTestCache) ClearPoolFailure(context.Context, int64) error {
	c.cleared++
	return nil
}
func (c *hfTestCache) RebuildPoolIndex(ctx context.Context, _ int64, loader func(context.Context) ([]HFCredentialRef, error)) error {
	if c.rebuildErr != nil {
		return c.rebuildErr
	}
	_, err := loader(ctx)
	return err
}

type hfTestAccounts struct {
	AccountRepository
	requested []int64
}

func (r *hfTestAccounts) GetByIDs(_ context.Context, ids []int64) ([]*Account, error) {
	r.requested = append([]int64(nil), ids...)
	accounts := make([]*Account, 0, len(ids))
	for _, id := range ids {
		accounts = append(accounts, &Account{
			ID: id, Name: "hf-" + strconv.FormatInt(id, 10), Platform: PlatformHuggingFace,
			Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
			Concurrency: 1, Priority: 50,
			Credentials: map[string]any{"api_key_ciphertext": "ciphertext"},
			Extra:       map[string]any{"hf_pool_id": "10", "hf_token_fingerprint": "fingerprint"},
		})
	}
	return accounts, nil
}

type hfTestProtector struct{ HFCredentialProtector }

func (hfTestProtector) Decrypt(string) (string, error)           { return "hf_valid_test_token", nil }
func (hfTestProtector) Encrypt(token string) (string, error)     { return "cipher:" + token, nil }
func (hfTestProtector) Fingerprint(token string) (string, error) { return "fingerprint:" + token, nil }

func newHFTestService(repo *hfTestRepo, cache *hfTestCache, accounts *hfTestAccounts) *HuggingFaceService {
	return NewHuggingFaceService(repo, cache, accounts, nil, hfTestProtector{}, &config.Config{
		HuggingFace: config.HuggingFaceConfig{
			Enabled: true, CandidatePoolSize: 64, CandidateScanSize: 128,
			MonthlyRecoveryTimezone: "Asia/Shanghai", MonthlyRecoveryHour: 9,
		},
	})
}

func TestHuggingFaceCandidateScanIsBoundedWith100kPriorities(t *testing.T) {
	priorities := make([]int, 100_000)
	for i := range priorities {
		priorities[i] = i
	}
	repo := &hfTestRepo{pools: []HuggingFacePool{{ID: 10, GroupID: 1, Status: StatusActive, Models: []string{"*"}}}}
	cache := &hfTestCache{priorities: priorities}
	accounts := &hfTestAccounts{}
	svc := newHFTestService(repo, cache, accounts)
	groupID := int64(1)

	result, err := svc.CandidateAccounts(context.Background(), &groupID, "meta-llama/Llama-3.3", nil)
	require.ErrorIs(t, err, ErrHFPoolHasNoCandidates)
	require.Empty(t, result)
	require.Equal(t, 128, cache.priorityLimit, "priority metadata reads must also remain bounded")
	require.Equal(t, 128, cache.rotateCalls, "empty priority buckets must still consume the bounded scan budget")
	require.Empty(t, accounts.requested)
}

func TestHuggingFaceCandidateHydrationAndDecryptionAreBounded(t *testing.T) {
	ids := make([]int64, 64)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	repo := &hfTestRepo{pools: []HuggingFacePool{{ID: 10, GroupID: 1, Status: StatusActive, BaseURL: HFDefaultBaseURL, Models: []string{"meta-llama/*"}}}}
	cache := &hfTestCache{priorities: []int{50}, rotateIDs: ids}
	accounts := &hfTestAccounts{}
	svc := newHFTestService(repo, cache, accounts)
	groupID := int64(1)

	result, err := svc.CandidateAccounts(context.Background(), &groupID, "meta-llama/Llama-3.3", nil)
	require.NoError(t, err)
	require.Len(t, result, 64)
	require.Len(t, accounts.requested, 64)
	for _, account := range result {
		require.Equal(t, "hf_valid_test_token", account.GetCredential("api_key"))
		require.Equal(t, HFDefaultBaseURL, account.GetCredential("base_url"))
		require.Empty(t, account.GetCredential("api_key_ciphertext"))
	}
}

func TestHuggingFaceImportReportsCommittedIndexPending(t *testing.T) {
	repo := &hfTestRepo{pools: []HuggingFacePool{{ID: 10, GroupID: 1, Status: StatusActive, Models: []string{"*"}}}}
	cache := &hfTestCache{rebuildErr: errors.New("redis unavailable")}
	svc := newHFTestService(repo, cache, &hfTestAccounts{})

	result, err := svc.ImportCredentials(context.Background(), 10, []HuggingFaceCredentialImport{{
		Token: "hf_valid_import_token", Priority: 50, Concurrency: 1,
	}})

	require.NoError(t, err, "a committed durable import must not be reported as failed")
	require.Equal(t, 1, result.Imported)
	require.True(t, result.IndexPending)
	require.Len(t, repo.imported, 1)
	_, pending := svc.pendingReconcile.Load(int64(10))
	require.True(t, pending)
}

func TestHuggingFaceImportRejectsMoreThan100kBeforeRepositoryWork(t *testing.T) {
	repo := &hfTestRepo{}
	svc := newHFTestService(repo, &hfTestCache{}, &hfTestAccounts{})

	result, err := svc.ImportCredentials(context.Background(), 10, make([]HuggingFaceCredentialImport, HFMaxCredentialImport+1))

	require.ErrorIs(t, err, ErrHFImportTooLarge)
	require.Nil(t, result)
	require.Empty(t, repo.imported)
}

func TestHuggingFaceFailureLifecycle(t *testing.T) {
	repo := &hfTestRepo{pools: []HuggingFacePool{{ID: 10, FailureThreshold: 5, CircuitCooldownSeconds: 30}}}
	cache := &hfTestCache{}
	svc := newHFTestService(repo, cache, &hfTestAccounts{})
	account := &Account{
		ID: 7, Platform: PlatformHuggingFace, Type: AccountTypeAPIKey,
		Extra: map[string]any{"hf_pool_id": "10", "hf_token_fingerprint": "fingerprint"},
	}

	failover := svc.ObserveHTTPFailure(context.Background(), account, http.StatusPaymentRequired, nil,
		[]byte(`{"error":"You have exceeded your monthly included credits"}`))
	require.NotNil(t, failover)
	require.Equal(t, StatusDisabled, repo.transition.Status)
	require.Equal(t, HFDisabledReasonMonthlyExhausted, repo.transition.Reason)
	require.NotNil(t, repo.transition.UpstreamStatusCode)
	require.Equal(t, http.StatusPaymentRequired, *repo.transition.UpstreamStatusCode)
	require.Contains(t, repo.transition.ErrorMessage, "HTTP 402")
	require.Contains(t, repo.transition.ErrorMessage, "monthly included credits")
	require.NotNil(t, repo.transition.RecoverAt)
	require.Equal(t, 1, cache.removed)

	failover = svc.ObserveHTTPFailure(context.Background(), account, http.StatusPaymentRequired, nil,
		[]byte(`{"error":{"message":"insufficient balance"}}`))
	require.NotNil(t, failover)
	require.Equal(t, StatusActive, repo.transition.Status)
	require.True(t, repo.transition.Schedulable)
	require.Equal(t, HFTemporaryReasonBillingRequired, repo.transition.Reason)
	require.NotNil(t, repo.transition.ReadyAt)
	require.NotNil(t, repo.transition.UpstreamStatusCode)
	require.Equal(t, http.StatusPaymentRequired, *repo.transition.UpstreamStatusCode)
	require.Equal(t, "HTTP 402: insufficient balance", repo.transition.ErrorMessage)
	require.Equal(t, 1, cache.cooled)

	failover = svc.ObserveHTTPFailure(context.Background(), account, http.StatusTooManyRequests,
		http.Header{"Retry-After": []string{"120"}}, nil)
	require.NotNil(t, failover)
	require.Equal(t, HFTemporaryReasonRateLimited, repo.transition.Reason)
	require.NotNil(t, repo.transition.ReadyAt)
	require.Equal(t, 2, cache.cooled)
	require.Equal(t, "120", failover.ResponseHeaders.Get("Retry-After"))

	failover = svc.ObserveHTTPFailure(context.Background(), account, http.StatusUnauthorized, nil, nil)
	require.NotNil(t, failover)
	require.Equal(t, HFDisabledReasonInvalidToken, repo.transition.Reason)
	require.Equal(t, 2, cache.removed)
}

func TestHuggingFaceModelWildcardAndMonthlyRecovery(t *testing.T) {
	require.True(t, wildcardMatch("meta-llama/*-Instruct", "meta-llama/Llama-3.3-Instruct"))
	require.True(t, wildcardMatch("Qwen/Qwen?-Coder", "Qwen/Qwen3-Coder"))
	require.False(t, wildcardMatch("meta-llama/*", "Qwen/Qwen3"))

	svc := newHFTestService(&hfTestRepo{}, &hfTestCache{}, &hfTestAccounts{})
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	recovery := svc.RecoverAtForMonthlyExhaustion(time.Date(2026, 8, 31, 23, 30, 0, 0, shanghai))
	require.Equal(t, time.Date(2026, 9, 1, 9, 0, 0, 0, shanghai), recovery)
}

func TestHuggingFaceSuccessClearsOnlyExpiredTemporaryState(t *testing.T) {
	repo := &hfTestRepo{}
	cache := &hfTestCache{}
	svc := newHFTestService(repo, cache, &hfTestAccounts{})
	past := time.Now().Add(-time.Second)
	account := &Account{
		ID: 7, Platform: PlatformHuggingFace, Type: AccountTypeAPIKey,
		Extra:        map[string]any{"hf_pool_id": "10", "hf_token_fingerprint": "fingerprint"},
		ErrorMessage: "old cooldown", RateLimitResetAt: &past,
	}

	svc.ObserveSuccess(context.Background(), account)
	require.Equal(t, StatusActive, repo.transition.Status)
	require.True(t, repo.transition.Schedulable)
	require.True(t, repo.transition.ClearTemporary)
	require.Nil(t, account.RateLimitResetAt)
	require.Empty(t, account.ErrorMessage)
	require.Equal(t, 1, cache.cleared)

	future := time.Now().Add(time.Minute)
	repo.transition = HFCredentialTransition{}
	account.RateLimitResetAt = &future
	svc.ObserveSuccess(context.Background(), account)
	require.Empty(t, repo.transition.Status, "an in-flight success must not erase a newer active cooldown")

	repo.transition = HFCredentialTransition{}
	account.RateLimitResetAt = &past
	account.TempUnschedulableUntil = &future
	svc.ObserveSuccess(context.Background(), account)
	require.Empty(t, repo.transition.Status, "one expired marker must not erase another active cooldown")
}

func TestHuggingFaceRawGatewayLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	newGateway := func(response *http.Response) (*OpenAIGatewayService, *hfTestRepo, *hfTestCache, *Account, *httpUpstreamRecorder, *gin.Context) {
		repo := &hfTestRepo{pools: []HuggingFacePool{{ID: 10, GroupID: 1, Status: StatusActive, FailureThreshold: 5, CircuitCooldownSeconds: 30}}}
		cache := &hfTestCache{}
		cfg := &config.Config{
			Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}},
			HuggingFace: config.HuggingFaceConfig{
				Enabled: true, CandidatePoolSize: 64, CandidateScanSize: 128,
				RateLimitCooldownSeconds: 30, BillingCooldownSeconds: 300, TransientCooldownSeconds: 15,
			},
		}
		hf := NewHuggingFaceService(repo, cache, &hfTestAccounts{}, nil, hfTestProtector{}, cfg)
		upstream := &httpUpstreamRecorder{resp: response}
		gateway := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, huggingFace: hf}
		account := &Account{
			ID: 7, Name: "hf-gateway-test", Platform: PlatformHuggingFace, Type: AccountTypeAPIKey,
			Status: StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"api_key": "hf_gateway_test_token", "base_url": "http://upstream.example"},
			Extra:       map[string]any{"hf_pool_id": "10", "hf_token_fingerprint": "fingerprint"},
		}
		recorder := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(recorder)
		body := []byte(`{"model":"meta-llama/test","messages":[{"role":"user","content":"hello"}],"stream":false}`)
		ginCtx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
		return gateway, repo, cache, account, upstream, ginCtx
	}

	t.Run("429 persists cooldown and uses only the selected HF bearer token", func(t *testing.T) {
		response := &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"42"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
		}
		gateway, repo, cache, account, upstream, ginCtx := newGateway(response)
		result, err := gateway.forwardAsRawChatCompletions(context.Background(), ginCtx, account, []byte(`{"model":"meta-llama/test","messages":[],"stream":false}`), "")
		require.Nil(t, result)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Equal(t, HFFailoverReason, failover.Reason)
		require.Equal(t, HFTemporaryReasonRateLimited, repo.transition.Reason)
		require.Equal(t, 1, cache.cooled)
		require.NotNil(t, upstream.lastReq)
		require.Equal(t, "Bearer hf_gateway_test_token", upstream.lastReq.Header.Get("Authorization"))
		require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	})

	t.Run("responses fallback 402 uses HF lifecycle instead of legacy permanent error", func(t *testing.T) {
		response := &http.Response{
			StatusCode: http.StatusPaymentRequired,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"insufficient balance"}}`)),
		}
		gateway, repo, cache, account, _, ginCtx := newGateway(response)
		body := []byte(`{"model":"meta-llama/test","input":"hello","stream":false}`)
		result, err := gateway.forwardResponsesViaRawChatCompletions(context.Background(), ginCtx, account, body)
		require.Nil(t, result)
		var failover *UpstreamFailoverError
		require.ErrorAs(t, err, &failover)
		require.Equal(t, HFFailoverReason, failover.Reason)
		require.Equal(t, StatusActive, repo.transition.Status)
		require.True(t, repo.transition.Schedulable)
		require.Equal(t, HFTemporaryReasonBillingRequired, repo.transition.Reason)
		require.NotNil(t, repo.transition.UpstreamStatusCode)
		require.Equal(t, http.StatusPaymentRequired, *repo.transition.UpstreamStatusCode)
		require.Equal(t, "HTTP 402: insufficient balance", repo.transition.ErrorMessage)
		require.NotNil(t, repo.transition.ReadyAt)
		require.Equal(t, 1, cache.cooled)
	})

	t.Run("complete 2xx body clears the pool breaker", func(t *testing.T) {
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chatcmpl_hf","object":"chat.completion","model":"meta-llama/test","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			)),
		}
		gateway, _, cache, account, _, ginCtx := newGateway(response)
		result, err := gateway.forwardAsRawChatCompletions(context.Background(), ginCtx, account, []byte(`{"model":"meta-llama/test","messages":[],"stream":false}`), "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, cache.cleared)
		require.Zero(t, cache.cooled)
	})

	t.Run("malformed 2xx body is a transient failure, not a success", func(t *testing.T) {
		response := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"broken":`)),
		}
		gateway, repo, cache, account, _, ginCtx := newGateway(response)
		result, err := gateway.forwardAsRawChatCompletions(context.Background(), ginCtx, account, []byte(`{"model":"meta-llama/test","messages":[],"stream":false}`), "")
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, HFTemporaryReasonTransient, repo.transition.Reason)
		require.Equal(t, 1, cache.cooled)
		require.Equal(t, 1, cache.failures)
		require.Zero(t, cache.cleared)
	})

	t.Run("unclassified client error leaves HF lifecycle unchanged", func(t *testing.T) {
		response := &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid model option"}}`)),
		}
		gateway, repo, cache, account, _, ginCtx := newGateway(response)
		result, err := gateway.forwardAsRawChatCompletions(context.Background(), ginCtx, account, []byte(`{"model":"meta-llama/test","messages":[]}`), "")
		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, http.StatusBadRequest, ginCtx.Writer.Status())
		require.Empty(t, repo.transition.Reason)
		require.Zero(t, cache.cooled)
		require.Zero(t, cache.failures)
	})

	t.Run("count tokens is estimated locally without an unsupported HF subrequest", func(t *testing.T) {
		gateway, _, _, account, upstream, ginCtx := newGateway(nil)
		body := []byte(`{"model":"meta-llama/test","messages":[{"role":"user","content":"hello"}]}`)
		err := gateway.ForwardCountTokensAsAnthropic(context.Background(), ginCtx, account, body, "")
		require.NoError(t, err)
		require.Nil(t, upstream.lastReq)
		require.Equal(t, http.StatusOK, ginCtx.Writer.Status())
		require.Positive(t, ginCtx.Writer.Size())
	})
}
