//go:build integration

package repository

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestHuggingFaceRepositoryImportsAndRotates100kCredentialsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name := fmt.Sprintf("hf-scale-%d", time.Now().UnixNano())
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO groups (name, platform, status) VALUES ($1,'huggingface','active') RETURNING id`, name,
	).Scan(&groupID))
	var poolID int64
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if poolID > 0 {
			_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE platform='huggingface' AND extra->>'hf_pool_id'=$1`, strconv.FormatInt(poolID, 10))
			_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM hf_pools WHERE id=$1`, poolID)
		}
		_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM groups WHERE id=$1`, groupID)
	})

	repo := &huggingFaceRepository{db: integrationDB}
	pool := &service.HuggingFacePool{
		GroupID: groupID, Name: "scale", BaseURL: service.HFDefaultBaseURL,
		Priority: 10, Weight: 100, Status: service.StatusActive, Models: []string{"*"},
		FailureThreshold: 3, CircuitCooldownSeconds: 20,
	}
	require.NoError(t, repo.CreatePool(ctx, pool))
	poolID = pool.ID

	credentials := make([]service.HFProtectedCredential, service.HFMaxCredentialImport)
	for i := range credentials {
		id := strconv.Itoa(i)
		credentials[i] = service.HFProtectedCredential{
			Fingerprint: name + "-" + id,
			Ciphertext:  "cipher-" + id,
			Suffix:      fmt.Sprintf("%06d", i%1_000_000),
			Priority:    50,
			Concurrency: 1,
		}
	}

	refs, duplicates, err := repo.ImportCredentials(ctx, pool.ID, credentials)
	require.NoError(t, err)
	require.Len(t, refs, service.HFMaxCredentialImport)
	require.Zero(t, duplicates)

	durableRefs, err := repo.ListCredentialRefs(ctx, pool.ID)
	require.NoError(t, err)
	require.Len(t, durableRefs, service.HFMaxCredentialImport)

	cache := &huggingFaceCache{rdb: testRedis(t)}
	require.NoError(t, cache.RebuildPoolIndex(ctx, pool.ID, func(context.Context) ([]service.HFCredentialRef, error) {
		return durableRefs, nil
	}))
	first, err := cache.RotateCandidates(ctx, pool.ID, 50, 64, time.Now())
	require.NoError(t, err)
	require.Len(t, first, 64)
	second, err := cache.RotateCandidates(ctx, pool.ID, 50, 64, time.Now())
	require.NoError(t, err)
	require.Len(t, second, 64)
	require.NotEqual(t, first, second)
}

func TestHuggingFaceRepositoryLifecycleIntegration(t *testing.T) {
	ctx := context.Background()
	name := fmt.Sprintf("hf-integration-%d", time.Now().UnixNano())
	var groupID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`INSERT INTO groups (name, platform, status) VALUES ($1,'huggingface','active') RETURNING id`, name,
	).Scan(&groupID))
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM accounts WHERE platform='huggingface' AND extra->>'hf_pool_id' IN (SELECT id::text FROM hf_pools WHERE group_id=$1)`, groupID)
		_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM hf_pools WHERE group_id=$1`, groupID)
		_, _ = integrationDB.ExecContext(cleanupCtx, `DELETE FROM groups WHERE id=$1`, groupID)
	})

	repo := &huggingFaceRepository{db: integrationDB}
	pool := &service.HuggingFacePool{
		GroupID: groupID, Name: "primary", BaseURL: service.HFDefaultBaseURL,
		Priority: 10, Weight: 100, Status: service.StatusActive, Models: []string{"meta-llama/*"},
		FailureThreshold: 3, CircuitCooldownSeconds: 20,
	}
	require.NoError(t, repo.CreatePool(ctx, pool))
	require.Positive(t, pool.ID)

	_, err := integrationDB.ExecContext(ctx, `UPDATE groups SET platform='openai' WHERE id=$1`, groupID)
	require.Error(t, err, "a group with an active HF pool must not change platform")
	_, err = integrationDB.ExecContext(ctx, `UPDATE groups SET deleted_at=NOW() WHERE id=$1`, groupID)
	require.Error(t, err, "a group with an active HF pool must not be deleted")

	credentials := []service.HFProtectedCredential{
		{Fingerprint: "fp-a-" + name, Ciphertext: "cipher-a", Suffix: "aaaaaa", Priority: 50, Concurrency: 1},
		{Fingerprint: "fp-b-" + name, Ciphertext: "cipher-b", Suffix: "bbbbbb", Priority: 50, Concurrency: 2},
		{Fingerprint: "fp-c-" + name, Ciphertext: "cipher-c", Suffix: "cccccc", Priority: 10, Concurrency: 1},
	}
	refs, duplicates, err := repo.ImportCredentials(ctx, pool.ID, credentials)
	require.NoError(t, err)
	require.Len(t, refs, 3)
	require.Zero(t, duplicates)

	second, duplicates, err := repo.ImportCredentials(ctx, pool.ID, []service.HFProtectedCredential{
		credentials[0],
		{Fingerprint: "fp-d-" + name, Ciphertext: "cipher-d", Suffix: "dddddd", Priority: 20, Concurrency: 1},
	})
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.Equal(t, 1, duplicates)

	pools, err := repo.ListPoolsByGroup(ctx, groupID, true)
	require.NoError(t, err)
	require.Len(t, pools, 1)
	require.EqualValues(t, 4, pools[0].CredentialCount)
	require.EqualValues(t, 4, pools[0].AvailableCount)

	// The database trigger is the last line of defense against leaking HF keys
	// into the legacy full-snapshot scheduler.
	_, err = integrationDB.ExecContext(ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1,$2)`, refs[0].AccountID, groupID)
	require.Error(t, err)

	// Use a sub-second boundary to verify the canonical fixed-precision
	// timestamp comparison: a credential must not recover early merely because
	// RFC3339 fractional fields have different string lengths.
	queryNow := time.Now().UTC().Truncate(time.Second).Add(100 * time.Millisecond)
	recoverAt := queryNow.Add(500 * time.Millisecond)
	paymentRequired := http.StatusPaymentRequired
	transition := service.HFCredentialTransition{
		Status: service.StatusDisabled, Schedulable: false,
		Reason: service.HFDisabledReasonMonthlyExhausted, ErrorMessage: "monthly exhausted",
		UpstreamStatusCode: &paymentRequired, RecoverAt: &recoverAt,
	}
	ref, applied, err := repo.TransitionCredential(ctx, refs[0].AccountID, credentials[0].Fingerprint, transition)
	require.NoError(t, err)
	require.True(t, applied)
	require.Equal(t, pool.ID, ref.PoolID)
	var storedStatusCode int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT (extra->>'hf_upstream_status_code')::integer FROM accounts WHERE id=$1`, refs[0].AccountID,
	).Scan(&storedStatusCode))
	require.Equal(t, http.StatusPaymentRequired, storedStatusCode)

	recovered, err := repo.RecoverDueCredentials(ctx, queryNow, 10)
	require.NoError(t, err)
	require.Empty(t, recovered, "a future sub-second recovery timestamp must not compare as due")

	recovered, err = repo.RecoverDueCredentials(ctx, recoverAt.Add(time.Nanosecond), 10)
	require.NoError(t, err)
	require.Len(t, recovered, 1)
	require.Equal(t, refs[0].AccountID, recovered[0].AccountID)

	// A late success from an older in-flight request must not erase a newer 429
	// cooldown (or reactivate a concurrently disabled credential).
	futureCooldown := time.Now().Add(time.Hour)
	_, applied, err = repo.TransitionCredential(ctx, refs[1].AccountID, credentials[1].Fingerprint, service.HFCredentialTransition{
		Status: service.StatusActive, Schedulable: true,
		Reason: service.HFTemporaryReasonRateLimited, ErrorMessage: "rate limited", ReadyAt: &futureCooldown,
	})
	require.NoError(t, err)
	require.True(t, applied)
	_, applied, err = repo.TransitionCredential(ctx, refs[1].AccountID, credentials[1].Fingerprint, service.HFCredentialTransition{
		Status: service.StatusActive, Schedulable: true, ClearTemporary: true,
	})
	require.NoError(t, err)
	require.False(t, applied, "conditional success cleanup must preserve a newer active cooldown")

	pastCooldown := time.Now().Add(-time.Second)
	_, applied, err = repo.TransitionCredential(ctx, refs[1].AccountID, credentials[1].Fingerprint, service.HFCredentialTransition{
		Status: service.StatusActive, Schedulable: true,
		Reason: service.HFTemporaryReasonRateLimited, ErrorMessage: "expired", ReadyAt: &pastCooldown,
	})
	require.NoError(t, err)
	require.True(t, applied)
	_, applied, err = repo.TransitionCredential(ctx, refs[1].AccountID, credentials[1].Fingerprint, service.HFCredentialTransition{
		Status: service.StatusActive, Schedulable: true, ClearTemporary: true,
	})
	require.NoError(t, err)
	require.True(t, applied, "conditional success cleanup should clear an expired cooldown")

	// Legacy HF rows may already contain only an error message from the generic
	// account error path. The admin list must still extract and expose its HTTP
	// status so operators do not see an unexplained `error` badge.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE accounts SET status='error', schedulable=false,
			error_message='Payment required (402): insufficient balance or billing issue',
			extra=extra-'hf_upstream_status_code'
		WHERE id=$1`, refs[0].AccountID)
	require.NoError(t, err)

	page, err := repo.ListCredentials(ctx, pool.ID, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 4, page.Total)
	require.Len(t, page.Items, 4)
	var legacyItem *service.HFCredentialListItem
	for _, item := range page.Items {
		require.NotEmpty(t, item.TokenSuffix)
		if item.AccountID == refs[0].AccountID {
			copyItem := item
			legacyItem = &copyItem
		}
	}
	require.NotNil(t, legacyItem)
	require.NotNil(t, legacyItem.UpstreamStatusCode)
	require.Equal(t, http.StatusPaymentRequired, *legacyItem.UpstreamStatusCode)
	require.Contains(t, legacyItem.ErrorMessage, "Payment required")

	require.NoError(t, repo.DeletePool(ctx, pool.ID))
	_, err = repo.GetPool(ctx, pool.ID)
	require.ErrorIs(t, err, service.ErrHFPoolNotFound)
	var remaining int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM accounts WHERE platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$1`, fmt.Sprint(pool.ID),
	).Scan(&remaining))
	require.Zero(t, remaining)
}
