package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newHFCacheTest(t *testing.T) (*huggingFaceCache, context.Context) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &huggingFaceCache{rdb: rdb}, context.Background()
}

func TestHuggingFaceCacheRotatesBoundedWindowAcross100kKeys(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	refs := make([]service.HFCredentialRef, 100_000)
	for i := range refs {
		refs[i] = service.HFCredentialRef{AccountID: int64(i + 1), PoolID: 11, Priority: 50}
	}
	require.NoError(t, cache.RebuildPoolIndex(ctx, 11, func(context.Context) ([]service.HFCredentialRef, error) { return refs, nil }))

	first, err := cache.RotateCandidates(ctx, 11, 50, 64, time.Now())
	require.NoError(t, err)
	require.Len(t, first, 64)
	second, err := cache.RotateCandidates(ctx, 11, 50, 64, time.Now())
	require.NoError(t, err)
	require.Len(t, second, 64)
	require.NotEqual(t, first, second)
	require.Equal(t, int64(1), first[0])
	require.Equal(t, int64(65), second[0])
}

func TestHuggingFaceCacheListsOnlyBoundedPriorities(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	refs := make([]service.HFCredentialRef, 1_024)
	for i := range refs {
		refs[i] = service.HFCredentialRef{AccountID: int64(i + 1), PoolID: 14, Priority: i}
	}
	require.NoError(t, cache.RebuildPoolIndex(ctx, 14, func(context.Context) ([]service.HFCredentialRef, error) { return refs, nil }))

	priorities, err := cache.ListPriorities(ctx, 14, 128)
	require.NoError(t, err)
	require.Len(t, priorities, 128)
	require.Equal(t, 0, priorities[0])
	require.Equal(t, 127, priorities[len(priorities)-1])
}

func TestHuggingFaceCacheRemovesEmptyPriorityBucket(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	refs := []service.HFCredentialRef{
		{AccountID: 1, PoolID: 15, Priority: 1},
		{AccountID: 2, PoolID: 15, Priority: 2},
	}
	require.NoError(t, cache.RebuildPoolIndex(ctx, 15, func(context.Context) ([]service.HFCredentialRef, error) { return refs, nil }))
	require.NoError(t, cache.RemoveCredential(ctx, refs[0]))

	priorities, err := cache.ListPriorities(ctx, 15, 10)
	require.NoError(t, err)
	require.Equal(t, []int{2}, priorities, "an empty lower-priority bucket must not starve live higher-priority keys")
}

func TestHuggingFaceCacheCooldownAndDuePromotion(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	ref := service.HFCredentialRef{AccountID: 9, PoolID: 12, Priority: 7}
	require.NoError(t, cache.RebuildPoolIndex(ctx, 12, func(context.Context) ([]service.HFCredentialRef, error) {
		return []service.HFCredentialRef{ref}, nil
	}))
	readyAt := time.Now().Add(time.Minute)
	require.NoError(t, cache.CooldownCredential(ctx, ref, readyAt))

	ids, err := cache.RotateCandidates(ctx, 12, 7, 1, readyAt.Add(-time.Millisecond))
	require.NoError(t, err)
	require.Empty(t, ids)
	ids, err = cache.RotateCandidates(ctx, 12, 7, 1, readyAt)
	require.NoError(t, err)
	require.Equal(t, []int64{9}, ids)
}

func TestHuggingFaceCacheMutationCannotBeLostBehindRebuild(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	ref := service.HFCredentialRef{AccountID: 5, PoolID: 13, Priority: 1}
	require.NoError(t, cache.RebuildPoolIndex(ctx, 13, func(context.Context) ([]service.HFCredentialRef, error) {
		return []service.HFCredentialRef{ref}, nil
	}))

	loaderEntered := make(chan struct{})
	releaseLoader := make(chan struct{})
	rebuildDone := make(chan error, 1)
	go func() {
		rebuildDone <- cache.RebuildPoolIndex(ctx, 13, func(context.Context) ([]service.HFCredentialRef, error) {
			close(loaderEntered)
			<-releaseLoader
			return []service.HFCredentialRef{ref}, nil
		})
	}()
	<-loaderEntered
	readyAt := time.Now().Add(time.Minute)
	cooldownDone := make(chan error, 1)
	go func() { cooldownDone <- cache.CooldownCredential(ctx, ref, readyAt) }()

	select {
	case err := <-cooldownDone:
		t.Fatalf("cooldown bypassed rebuild mutation lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseLoader)
	require.NoError(t, <-rebuildDone)
	require.NoError(t, <-cooldownDone)
	ids, err := cache.RotateCandidates(ctx, 13, 1, 1, time.Now())
	require.NoError(t, err)
	require.Empty(t, ids)
}

func TestHuggingFaceCacheSmoothWeightedPoolsAndCircuitBreaker(t *testing.T) {
	cache, ctx := newHFCacheTest(t)
	pools := []service.HuggingFacePool{{ID: 1, Weight: 1}, {ID: 2, Weight: 3}}
	counts := map[int64]int{}
	for i := 0; i < 40; i++ {
		selected, err := cache.PickWeightedPool(ctx, 99, pools)
		require.NoError(t, err)
		counts[selected]++
	}
	require.Equal(t, 10, counts[1])
	require.Equal(t, 30, counts[2])

	pool := service.HuggingFacePool{ID: 21, FailureThreshold: 2, CircuitCooldownSeconds: 30}
	require.NoError(t, cache.RecordPoolFailure(ctx, pool))
	remaining, err := cache.PoolCooldownRemaining(ctx, pool.ID)
	require.NoError(t, err)
	require.Zero(t, remaining)
	require.NoError(t, cache.RecordPoolFailure(ctx, pool))
	remaining, err = cache.PoolCooldownRemaining(ctx, pool.ID)
	require.NoError(t, err)
	require.Greater(t, remaining, time.Duration(0))
	require.NoError(t, cache.ClearPoolFailure(ctx, pool.ID))
	remaining, err = cache.PoolCooldownRemaining(ctx, pool.ID)
	require.NoError(t, err)
	require.Zero(t, remaining)
}
