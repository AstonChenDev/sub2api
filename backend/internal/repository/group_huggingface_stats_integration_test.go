//go:build integration

package repository

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *GroupRepoSuite) TestHuggingFaceGroupCountsAndCapacity() {
	newGroup := func(name, platform string) int64 {
		g := &service.Group{Name: name, Platform: platform, Status: service.StatusActive, RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard}
		s.Require().NoError(s.repo.Create(s.ctx, g))
		return g.ID
	}
	hfGroup := newGroup("hf-stats-main", service.PlatformHuggingFace)
	otherGroup := newGroup("hf-stats-other", service.PlatformHuggingFace)
	emptyGroup := newGroup("hf-stats-empty", service.PlatformHuggingFace)
	ordinaryGroup := newGroup("hf-stats-ordinary", service.PlatformOpenAI)
	newPool := func(groupID int64, name, status string, deleted bool) int64 {
		var id int64
		s.Require().NoError(scanSingleRow(s.ctx, s.tx,
			`INSERT INTO hf_pools (group_id, name, status, deleted_at) VALUES ($1,$2,$3,CASE WHEN $4 THEN NOW() ELSE NULL END) RETURNING id`,
			[]any{groupID, name, status, deleted}, &id))
		return id
	}
	poolA := newPool(hfGroup, "a", "active", false)
	poolB := newPool(hfGroup, "b", "active", false)
	poolDisabled := newPool(hfGroup, "disabled", "disabled", false)
	poolDeleted := newPool(hfGroup, "deleted", "disabled", true)
	poolOther := newPool(otherGroup, "other", "active", false)

	insert := func(poolID int64, concurrency int, changes string) int64 {
		var id int64
		s.Require().NoError(scanSingleRow(s.ctx, s.tx,
			`INSERT INTO accounts (name, platform, type, status, schedulable, concurrency, extra)
			 VALUES ('hf-stats','huggingface','apikey','active',true,$1,jsonb_build_object('hf_pool_id',$2::text)) RETURNING id`,
			[]any{concurrency, strconv.FormatInt(poolID, 10)}, &id))
		if changes != "" {
			_, err := s.tx.ExecContext(s.ctx, "UPDATE accounts SET "+changes+" WHERE id=$1", id)
			s.Require().NoError(err)
		}
		return id
	}
	readyA := insert(poolA, 2, "")
	readyB := insert(poolB, 5, "")
	insert(poolA, 3, "rate_limit_reset_at=NOW()+INTERVAL '1 hour'")
	insert(poolA, 3, "temp_unschedulable_until=NOW()+INTERVAL '1 hour'")
	insert(poolA, 3, "overload_until=NOW()+INTERVAL '1 hour'")
	insert(poolA, 3, "rate_limit_reset_at=NOW()+INTERVAL '1 hour', temp_unschedulable_until=NOW()+INTERVAL '2 hours'")
	past := insert(poolA, 7, "rate_limit_reset_at=NOW()-INTERVAL '1 hour', temp_unschedulable_until=NOW()-INTERVAL '1 hour', overload_until=NOW()-INTERVAL '1 hour'")
	insert(poolA, 3, "status='disabled'")
	insert(poolA, 3, "schedulable=false")
	insert(poolA, 3, "expires_at=NOW()-INTERVAL '1 hour', auto_pause_on_expired=true")
	noPause := insert(poolA, 11, "expires_at=NOW()-INTERVAL '1 hour', auto_pause_on_expired=false")
	insert(poolDisabled, 99, "")
	insert(poolDisabled, 99, "rate_limit_reset_at=NOW()+INTERVAL '1 hour'")
	insert(poolA, 99, "deleted_at=NOW()")
	insert(poolDeleted, 99, "")
	insert(-1, 99, "") // orphan metadata must not enter any group
	insert(poolA, 99, "extra=jsonb_build_object('hf_pool_id','invalid')")
	insert(poolA, 99, "platform='openai'") // metadata alone does not make an HF credential
	otherID := insert(poolOther, 13, "")
	ordinaryID := insert(-1, 4, "platform='openai'")
	_, err := s.tx.ExecContext(s.ctx, `INSERT INTO account_groups (account_id, group_id) VALUES ($1,$2)`, ordinaryID, ordinaryGroup)
	s.Require().NoError(err)

	counts, err := s.repo.loadAccountCounts(s.ctx, []int64{hfGroup, otherGroup, emptyGroup, ordinaryGroup})
	s.Require().NoError(err)
	s.Equal(groupAccountCounts{Total: 13, Active: 4, RateLimited: 4}, counts[hfGroup])
	s.Equal(groupAccountCounts{Total: 1, Active: 1}, counts[otherGroup])
	s.Equal(groupAccountCounts{}, counts[emptyGroup])
	s.Equal(groupAccountCounts{Total: 1, Active: 1}, counts[ordinaryGroup])
	detail, err := s.repo.GetByID(s.ctx, hfGroup)
	s.Require().NoError(err)
	s.Equal(int64(13), detail.AccountCount)
	s.Equal(int64(4), detail.ActiveAccountCount)
	s.Equal(int64(4), detail.RateLimitedAccountCount)
	total, active, err := s.repo.GetAccountCount(s.ctx, hfGroup)
	s.Require().NoError(err)
	s.Equal(detail.AccountCount, total)
	s.Equal(detail.ActiveAccountCount, active)
	for _, order := range []string{"asc", "desc"} {
		groups, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page: 1, PageSize: 1, SortBy: "account_count", SortOrder: order,
		}, service.PlatformHuggingFace, "", "hf-stats-", nil)
		s.Require().NoError(err)
		s.Require().Len(groups, 1)
		if order == "desc" {
			s.Equal(hfGroup, groups[0].ID)
			s.Equal(int64(13), groups[0].AccountCount)
			s.Equal(int64(4), groups[0].RateLimitedAccountCount)
		} else {
			s.Equal(emptyGroup, groups[0].ID)
		}
	}

	rows, err := s.repo.ListHuggingFaceCapacityByGroupIDs(s.ctx, []int64{hfGroup, otherGroup, emptyGroup, ordinaryGroup})
	s.Require().NoError(err)
	s.ElementsMatch([]service.GroupAccountCapacityRow{
		{GroupID: hfGroup, AccountID: readyA, Concurrency: 2},
		{GroupID: hfGroup, AccountID: readyB, Concurrency: 5},
		{GroupID: hfGroup, AccountID: past, Concurrency: 7},
		{GroupID: hfGroup, AccountID: noPause, Concurrency: 11},
		{GroupID: otherGroup, AccountID: otherID, Concurrency: 13},
	}, rows)
	// Exercise the complete service with real Redis slots and both repository
	// projections: HF must be added exactly once and ordinary capacity preserved.
	cache := NewConcurrencyCache(testRedis(s.T()), 5, 30)
	for _, id := range []int64{readyA, readyB, ordinaryID} {
		requestID := fmt.Sprintf("hf-stats-%d-%d", id, time.Now().UnixNano())
		acquired, err := cache.AcquireAccountSlot(s.ctx, id, 2, requestID)
		s.Require().NoError(err)
		s.Require().True(acquired)
		s.T().Cleanup(func() { _ = cache.ReleaseAccountSlot(s.ctx, id, requestID) })
	}
	accounts := newAccountRepositoryWithSQL(s.tx.Client(), s.tx, nil)
	svc := service.NewGroupCapacityService(accounts, s.repo, service.NewConcurrencyService(cache), nil, nil)
	capacity, err := svc.GetAllGroupCapacity(s.ctx)
	s.Require().NoError(err)
	byGroup := make(map[int64]service.GroupCapacitySummary)
	for _, item := range capacity {
		byGroup[item.GroupID] = item
	}
	s.Equal(service.GroupCapacitySummary{GroupID: hfGroup, ConcurrencyMax: 25, ConcurrencyUsed: 2}, byGroup[hfGroup])
	s.Equal(service.GroupCapacitySummary{GroupID: otherGroup, ConcurrencyMax: 13}, byGroup[otherGroup])
	s.Equal(service.GroupCapacitySummary{GroupID: ordinaryGroup, ConcurrencyMax: 4, ConcurrencyUsed: 1}, byGroup[ordinaryGroup])
	s.Equal(service.GroupCapacitySummary{GroupID: emptyGroup}, byGroup[emptyGroup])
	rows, err = s.repo.ListHuggingFaceCapacityByGroupIDs(s.ctx, nil)
	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *GroupRepoSuite) TestHuggingFaceGroupStats100k() {
	g := &service.Group{Name: "hf-stats-scale", Platform: service.PlatformHuggingFace, Status: service.StatusActive, RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard}
	s.Require().NoError(s.repo.Create(s.ctx, g))
	var poolID int64
	s.Require().NoError(scanSingleRow(s.ctx, s.tx,
		`INSERT INTO hf_pools (group_id,name) VALUES ($1,'scale') RETURNING id`, []any{g.ID}, &poolID))
	_, err := s.tx.ExecContext(s.ctx, `INSERT INTO accounts (name,platform,type,status,schedulable,concurrency,extra)
		SELECT 'hf-scale-' || n, 'huggingface','apikey','active',true,2,
		jsonb_build_object('hf_pool_id',$1::text) FROM generate_series(1,100000) n`, strconv.FormatInt(poolID, 10))
	s.Require().NoError(err)
	started := time.Now()
	total, active, err := s.repo.GetAccountCount(s.ctx, g.ID)
	s.Require().NoError(err)
	s.Equal(int64(100000), total)
	s.Equal(total, active)
	cache := NewConcurrencyCache(testRedis(s.T()), 5, 30)
	svc := service.NewGroupCapacityService(newAccountRepositoryWithSQL(s.tx.Client(), s.tx, nil), s.repo, service.NewConcurrencyService(cache), nil, nil)
	capacity, err := svc.GetAllGroupCapacity(s.ctx)
	s.Require().NoError(err)
	var found bool
	for _, item := range capacity {
		if item.GroupID == g.ID {
			found = true
			s.Equal(service.GroupCapacitySummary{GroupID: g.ID, ConcurrencyMax: 200000}, item)
		}
	}
	s.True(found)
	s.T().Logf("100k HF group count and capacity reporting completed in %s", time.Since(started))
}
