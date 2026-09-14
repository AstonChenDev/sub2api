package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type hfCapacityGroupRepoStub struct {
	groupCapacityGroupRepoStub
	rows      []GroupAccountCapacityRow
	err       error
	requested []int64
}

func (s *hfCapacityGroupRepoStub) ListHuggingFaceCapacityByGroupIDs(_ context.Context, ids []int64) ([]GroupAccountCapacityRow, error) {
	s.requested = append([]int64(nil), ids...)
	return s.rows, s.err
}

type hfCapacityCacheStub struct {
	ConcurrencyCache
	batches [][]int64
	err     error
}

func (s *hfCapacityCacheStub) GetAccountConcurrencyBatch(_ context.Context, ids []int64) (map[int64]int, error) {
	s.batches = append(s.batches, append([]int64(nil), ids...))
	counts := make(map[int64]int, len(ids))
	for _, id := range ids {
		counts[id] = 1
	}
	return counts, s.err
}

func TestGroupCapacityHuggingFaceMergesAndBatches(t *testing.T) {
	groups := &hfCapacityGroupRepoStub{groupCapacityGroupRepoStub: groupCapacityGroupRepoStub{groupIDs: []int64{10, 20, 30, 40}}}
	// More than two batches, spanning pools/groups without loading credentials.
	for id := int64(1); id <= 1025; id++ {
		groups.rows = append(groups.rows, GroupAccountCapacityRow{GroupID: 20, AccountID: id, Concurrency: 3})
	}
	groups.rows = append(groups.rows,
		GroupAccountCapacityRow{GroupID: 30, AccountID: 1026, Concurrency: 5},
		GroupAccountCapacityRow{GroupID: 20, AccountID: 1, Concurrency: 3},    // duplicate
		GroupAccountCapacityRow{GroupID: 99, AccountID: 2000, Concurrency: 9}, // unrelated
		GroupAccountCapacityRow{GroupID: 20, AccountID: 0, Concurrency: 9},
	)
	accounts := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{{GroupID: 10, AccountID: 3000, Concurrency: 7}}}
	cache := &hfCapacityCacheStub{}
	svc := NewGroupCapacityService(accounts, groups, NewConcurrencyService(cache), nil, nil)
	got, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Equal(t, groups.groupIDs, groups.requested)
	require.Equal(t, []GroupCapacitySummary{
		{GroupID: 10, ConcurrencyMax: 7, ConcurrencyUsed: 1},
		{GroupID: 20, ConcurrencyMax: 3075, ConcurrencyUsed: 1025},
		{GroupID: 30, ConcurrencyMax: 5, ConcurrencyUsed: 1},
		{GroupID: 40},
	}, got)
	require.Len(t, cache.batches, 4) // ordinary batch + three HF batches
	require.Len(t, cache.batches[1], 512)
	require.Len(t, cache.batches[2], 512)
	require.Len(t, cache.batches[3], 2)
}

func TestGroupCapacityHuggingFaceFailsOnUnavailableStats(t *testing.T) {
	wantErr := errors.New("stats unavailable")
	for _, failure := range []string{"repository", "redis", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			groups := &hfCapacityGroupRepoStub{
				groupCapacityGroupRepoStub: groupCapacityGroupRepoStub{groupIDs: []int64{10}},
				rows:                       []GroupAccountCapacityRow{{GroupID: 10, AccountID: 1, Concurrency: 2}},
			}
			cache := &hfCapacityCacheStub{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := wantErr
			switch failure {
			case "repository":
				groups.err = wantErr
			case "redis":
				cache.err = wantErr
			case "cancelled":
				cancel()
				expected = context.Canceled
			}
			svc := NewGroupCapacityService(&groupCapacityAccountRepoStub{}, groups, NewConcurrencyService(cache), nil, nil)
			got, err := svc.GetAllGroupCapacity(ctx)
			require.ErrorIs(t, err, expected)
			require.Nil(t, got, "unavailable stats must not be presented as zero usage")
		})
	}
}
