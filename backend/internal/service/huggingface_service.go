package service

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

type hfPoolCacheEntry struct {
	pools     []HuggingFacePool
	expiresAt time.Time
}

// HuggingFaceService is an isolated, opt-in two-stage scheduler:
// group -> weighted/priority pool -> bounded credential window. It never reads
// or rewrites the legacy scheduler snapshot.
type HuggingFaceService struct {
	repo      HuggingFaceRepository
	cache     HuggingFaceCache
	accounts  AccountRepository
	groups    GroupRepository
	protector HFCredentialProtector
	cfg       config.HuggingFaceConfig

	poolCache        sync.Map
	pendingReconcile sync.Map
	poolSF           singleflight.Group
	indexSF          singleflight.Group
	startOnce        sync.Once
	stopOnce         sync.Once
	stopCh           chan struct{}
}

func NewHuggingFaceService(
	repo HuggingFaceRepository,
	cache HuggingFaceCache,
	accounts AccountRepository,
	groups GroupRepository,
	protector HFCredentialProtector,
	cfg *config.Config,
) *HuggingFaceService {
	hfCfg := config.HuggingFaceConfig{}
	if cfg != nil {
		hfCfg = cfg.HuggingFace
	}
	applyHuggingFaceRuntimeDefaults(&hfCfg)
	return &HuggingFaceService{
		repo: repo, cache: cache, accounts: accounts, groups: groups,
		protector: protector, cfg: hfCfg, stopCh: make(chan struct{}),
	}
}

func applyHuggingFaceRuntimeDefaults(cfg *config.HuggingFaceConfig) {
	if cfg.CandidatePoolSize <= 0 {
		cfg.CandidatePoolSize = 64
	}
	if cfg.CandidateScanSize < cfg.CandidatePoolSize {
		cfg.CandidateScanSize = max(128, cfg.CandidatePoolSize)
	}
	if cfg.PoolMetadataCacheSeconds <= 0 {
		cfg.PoolMetadataCacheSeconds = 5
	}
	if cfg.RateLimitCooldownSeconds <= 0 {
		cfg.RateLimitCooldownSeconds = 30
	}
	if cfg.BillingCooldownSeconds <= 0 {
		cfg.BillingCooldownSeconds = 300
	}
	if cfg.TransientCooldownSeconds <= 0 {
		cfg.TransientCooldownSeconds = 15
	}
	if cfg.MaxRetryAfterSeconds <= 0 {
		cfg.MaxRetryAfterSeconds = 3600
	}
	if cfg.RecoveryScanIntervalSeconds <= 0 {
		cfg.RecoveryScanIntervalSeconds = 60
	}
	if cfg.ReconcileIntervalSeconds <= 0 {
		cfg.ReconcileIntervalSeconds = 600
	}
	if strings.TrimSpace(cfg.MonthlyRecoveryTimezone) == "" {
		cfg.MonthlyRecoveryTimezone = "Asia/Shanghai"
	}
	if len(cfg.AllowedBaseURLs) == 0 {
		cfg.AllowedBaseURLs = []string{HFDefaultBaseURL}
	}
}

func (s *HuggingFaceService) Enabled() bool {
	return s != nil && s.cfg.Enabled && s.repo != nil && s.cache != nil && s.accounts != nil && s.protector != nil
}

func (s *HuggingFaceService) MaxAccountSwitches() int {
	if s == nil || s.cfg.MaxAccountSwitches < 0 {
		return 0
	}
	return s.cfg.MaxAccountSwitches
}

func (s *HuggingFaceService) requireEnabled() error {
	if !s.Enabled() {
		return ErrHFFeatureDisabled
	}
	return nil
}

// Start begins bounded recovery and reconciliation loops. It is safe to call
// more than once because providers create one singleton service.
func (s *HuggingFaceService) Start() {
	if !s.Enabled() {
		return
	}
	s.startOnce.Do(func() { go s.runMaintenance() })
}

func (s *HuggingFaceService) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *HuggingFaceService) runMaintenance() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	if err := s.ReconcileAll(ctx); err != nil {
		logger.L().Warn("hugging face initial index reconciliation failed", zap.Error(err))
	}
	cancel()

	recoveryTicker := time.NewTicker(time.Duration(s.cfg.RecoveryScanIntervalSeconds) * time.Second)
	reconcileTicker := time.NewTicker(time.Duration(s.cfg.ReconcileIntervalSeconds) * time.Second)
	defer recoveryTicker.Stop()
	defer reconcileTicker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-recoveryTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			if _, err := s.RecoverDue(ctx, 100_000); err != nil {
				logger.L().Warn("hugging face credential recovery failed", zap.Error(err))
			}
			cancel()
		case <-reconcileTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			if err := s.ReconcileAll(ctx); err != nil {
				logger.L().Warn("hugging face index reconciliation failed", zap.Error(err))
			}
			cancel()
		}
	}
}

func (s *HuggingFaceService) validateGroup(ctx context.Context, groupID int64) error {
	if groupID <= 0 || s.groups == nil {
		return fmt.Errorf("group_id is required")
	}
	group, err := s.groups.GetByID(ctx, groupID)
	if err != nil {
		return err
	}
	if group == nil || group.Platform != PlatformHuggingFace {
		return fmt.Errorf("group %d must use platform %q", groupID, PlatformHuggingFace)
	}
	if group.Status != StatusActive {
		return fmt.Errorf("group %d is not active", groupID)
	}
	return nil
}

func (s *HuggingFaceService) normalizePoolInput(ctx context.Context, input HuggingFacePoolInput) (HuggingFacePool, error) {
	if err := s.requireEnabled(); err != nil {
		return HuggingFacePool{}, err
	}
	if err := s.validateGroup(ctx, input.GroupID); err != nil {
		return HuggingFacePool{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len([]rune(name)) > 100 {
		return HuggingFacePool{}, fmt.Errorf("name must contain between 1 and 100 characters")
	}
	baseURL, err := s.normalizeBaseURL(input.BaseURL)
	if err != nil {
		return HuggingFacePool{}, err
	}
	status := strings.TrimSpace(input.Status)
	if status == "" {
		status = StatusActive
	}
	if status != StatusActive && status != StatusDisabled {
		return HuggingFacePool{}, fmt.Errorf("status must be active or disabled")
	}
	weight := input.Weight
	if weight == 0 {
		weight = 100
	}
	if input.Priority < 0 || input.Priority > 1_000_000 || weight < 1 || weight > 1_000_000 {
		return HuggingFacePool{}, fmt.Errorf("priority or weight is outside the allowed range")
	}
	models, err := normalizeHFModelPatterns(input.Models)
	if err != nil {
		return HuggingFacePool{}, err
	}
	failureThreshold := input.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = 5
	}
	circuitSeconds := input.CircuitCooldownSeconds
	if circuitSeconds == 0 {
		circuitSeconds = 30
	}
	if failureThreshold < 1 || failureThreshold > 1000 || circuitSeconds < 1 || circuitSeconds > 86400 {
		return HuggingFacePool{}, fmt.Errorf("invalid circuit breaker settings")
	}
	return HuggingFacePool{
		GroupID: input.GroupID, Name: name, BaseURL: baseURL,
		Priority: input.Priority, Weight: weight, Status: status, Models: models,
		FailureThreshold: failureThreshold, CircuitCooldownSeconds: circuitSeconds,
	}, nil
}

func (s *HuggingFaceService) normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = HFDefaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("base_url must be an allowed HTTPS origin")
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	if path != "" && path != "/v1" {
		return "", fmt.Errorf("base_url path must be empty or /v1")
	}
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	normalized := strings.TrimRight(u.String(), "/")
	allowed := false
	for _, candidate := range s.cfg.AllowedBaseURLs {
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
		if normalized == candidate || strings.TrimSuffix(normalized, "/v1") == strings.TrimSuffix(candidate, "/v1") {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", fmt.Errorf("base_url is not present in huggingface.allowed_base_urls")
	}
	if strings.TrimSuffix(normalized, "/v1") == HFDefaultBaseURL {
		return HFDefaultBaseURL, nil
	}
	return normalized, nil
}

func normalizeHFModelPatterns(models []string) ([]string, error) {
	if len(models) == 0 {
		return []string{"*"}, nil
	}
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || len(model) > 256 || strings.ContainsAny(model, "\r\n\x00") {
			return nil, fmt.Errorf("models contains an invalid pattern")
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		out = append(out, model)
	}
	if len(out) > 512 {
		return nil, fmt.Errorf("models may contain at most 512 patterns")
	}
	sort.Strings(out)
	return out, nil
}

func (s *HuggingFaceService) CreatePool(ctx context.Context, input HuggingFacePoolInput) (*HuggingFacePool, error) {
	pool, err := s.normalizePoolInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreatePool(ctx, &pool); err != nil {
		return nil, err
	}
	s.invalidateGroupPools(pool.GroupID)
	if err := s.cache.RebuildPoolIndex(ctx, pool.ID, func(context.Context) ([]HFCredentialRef, error) { return nil, nil }); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_ = s.repo.DeletePool(cleanupCtx, pool.ID)
		cancel()
		return nil, fmt.Errorf("initialize hugging face pool index: %w", err)
	}
	return &pool, nil
}

func (s *HuggingFaceService) UpdatePool(ctx context.Context, id int64, input HuggingFacePoolInput) (*HuggingFacePool, error) {
	pool, err := s.normalizePoolInput(ctx, input)
	if err != nil {
		return nil, err
	}
	old, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return nil, err
	}
	if old.GroupID != pool.GroupID {
		return nil, fmt.Errorf("moving a pool between groups is not supported")
	}
	pool.ID = id
	if err := s.repo.UpdatePool(ctx, &pool); err != nil {
		return nil, err
	}
	s.invalidateGroupPools(pool.GroupID)
	return s.repo.GetPool(ctx, id)
}

func (s *HuggingFaceService) DeletePool(ctx context.Context, id int64) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	pool, err := s.repo.GetPool(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.DeletePool(ctx, id); err != nil {
		return err
	}
	s.invalidateGroupPools(pool.GroupID)
	s.pendingReconcile.Delete(id)
	if err := s.cache.RebuildPoolIndex(ctx, id, func(context.Context) ([]HFCredentialRef, error) { return nil, nil }); err != nil {
		// The pool is already durably deleted and can no longer be selected from
		// metadata. A stale Redis generation is harmless and expires on cleanup.
		logger.L().Warn("clean up deleted hugging face pool index", zap.Int64("pool_id", id), zap.Error(err))
	}
	return nil
}

func (s *HuggingFaceService) ListPools(ctx context.Context, groupID int64, withStats bool) ([]HuggingFacePool, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if err := s.validateGroup(ctx, groupID); err != nil {
		return nil, err
	}
	return s.repo.ListPoolsByGroup(ctx, groupID, withStats)
}

func (s *HuggingFaceService) GetPool(ctx context.Context, id int64) (*HuggingFacePool, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	return s.repo.GetPool(ctx, id)
}

func (s *HuggingFaceService) ImportCredentials(ctx context.Context, poolID int64, entries []HuggingFaceCredentialImport) (*HFImportResult, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if len(entries) > HFMaxCredentialImport {
		return nil, ErrHFImportTooLarge
	}
	pool, err := s.repo.GetPool(ctx, poolID)
	if err != nil {
		return nil, err
	}
	result := &HFImportResult{Received: len(entries)}
	protected := make([]HFProtectedCredential, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		token := strings.TrimSpace(entry.Token)
		if !validHFToken(token) || entry.Priority < 0 || entry.Priority > 1_000_000 || entry.Concurrency < 0 || entry.Concurrency > 100_000 {
			result.Invalid++
			continue
		}
		fingerprint, err := s.protector.Fingerprint(token)
		if err != nil {
			return nil, fmt.Errorf("fingerprint credential: %w", err)
		}
		if _, duplicate := seen[fingerprint]; duplicate {
			result.Duplicate++
			continue
		}
		seen[fingerprint] = struct{}{}
		ciphertext, err := s.protector.Encrypt(token)
		if err != nil {
			return nil, fmt.Errorf("encrypt credential: %w", err)
		}
		priority := entry.Priority
		concurrency := entry.Concurrency
		if concurrency == 0 {
			concurrency = 1
		}
		protected = append(protected, HFProtectedCredential{
			Fingerprint: fingerprint, Ciphertext: ciphertext,
			Suffix: tokenSuffix(token), Priority: priority, Concurrency: concurrency,
		})
	}
	inserted, duplicateCount, err := s.repo.ImportCredentials(ctx, poolID, protected)
	if err != nil {
		return nil, err
	}
	result.Imported = len(inserted)
	result.Duplicate += duplicateCount
	if len(inserted) > 0 {
		// One generation rebuild is substantially cheaper and safer than 100k
		// per-key Redis lock/round trips. The durable import is already committed;
		// generation publication makes the whole batch visible atomically.
		if reconcileErr := s.ReconcilePool(ctx, pool.ID); reconcileErr != nil {
			s.pendingReconcile.Store(pool.ID, struct{}{})
			result.IndexPending = true
			logger.L().Warn("hugging face import committed with index reconciliation pending", zap.Int64("pool_id", pool.ID), zap.Error(reconcileErr))
			return result, nil
		}
		s.pendingReconcile.Delete(pool.ID)
	}
	return result, nil
}

func validHFToken(token string) bool {
	if len(token) < 8 || len(token) > 512 || !strings.HasPrefix(token, "hf_") {
		return false
	}
	for _, r := range token {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func tokenSuffix(token string) string {
	const visible = 6
	if len(token) <= visible {
		return token
	}
	return token[len(token)-visible:]
}

func (s *HuggingFaceService) ListCredentials(ctx context.Context, poolID int64, limit, offset int) (*HFCredentialPage, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if _, err := s.repo.GetPool(ctx, poolID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.ListCredentials(ctx, poolID, limit, offset)
}

func (s *HuggingFaceService) RecoverCredential(ctx context.Context, poolID, accountID int64) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	ref, err := s.repo.RecoverCredential(ctx, poolID, accountID)
	if err != nil {
		return err
	}
	if err := s.cache.AddCredential(ctx, ref); err != nil {
		s.pendingReconcile.Store(poolID, struct{}{})
		logger.L().Warn("hugging face credential recovered with index reconciliation pending", zap.Int64("pool_id", poolID), zap.Int64("account_id", accountID), zap.Error(err))
	}
	return nil
}

func (s *HuggingFaceService) DeleteCredential(ctx context.Context, poolID, accountID int64) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	ref, err := s.repo.DeleteCredential(ctx, poolID, accountID)
	if err != nil {
		return err
	}
	if err := s.cache.RemoveCredential(ctx, ref); err != nil {
		s.pendingReconcile.Store(poolID, struct{}{})
		logger.L().Warn("hugging face credential deleted with index reconciliation pending", zap.Int64("pool_id", poolID), zap.Int64("account_id", accountID), zap.Error(err))
	}
	return nil
}

func (s *HuggingFaceService) ReconcilePool(ctx context.Context, poolID int64) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if _, err := s.repo.GetPool(ctx, poolID); err != nil {
		return err
	}
	return s.cache.RebuildPoolIndex(ctx, poolID, func(loadCtx context.Context) ([]HFCredentialRef, error) {
		return s.repo.ListCredentialRefs(loadCtx, poolID)
	})
}

func (s *HuggingFaceService) ensurePoolIndex(ctx context.Context, poolID int64) error {
	key := strconv.FormatInt(poolID, 10)
	_, err, _ := s.indexSF.Do(key, func() (any, error) {
		hasIndex, cacheErr := s.cache.HasPoolIndex(ctx, poolID)
		if cacheErr != nil {
			return nil, cacheErr
		}
		if hasIndex {
			return nil, nil
		}
		return nil, s.ReconcilePool(ctx, poolID)
	})
	return err
}

func (s *HuggingFaceService) ReconcileAll(ctx context.Context) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	pools, err := s.repo.ListActivePools(ctx)
	if err != nil {
		return err
	}
	for _, pool := range pools {
		if err := s.ReconcilePool(ctx, pool.ID); err != nil {
			return fmt.Errorf("reconcile pool %d: %w", pool.ID, err)
		}
	}
	return nil
}

func (s *HuggingFaceService) RecoverDue(ctx context.Context, limit int) (int, error) {
	if err := s.requireEnabled(); err != nil {
		return 0, err
	}
	if limit <= 0 || limit > 100_000 {
		limit = 10_000
	}
	poolIDs := make(map[int64]struct{})
	s.pendingReconcile.Range(func(key, _ any) bool {
		if poolID, ok := key.(int64); ok && poolID > 0 {
			poolIDs[poolID] = struct{}{}
		}
		return true
	})
	total := 0
	for total < limit {
		batchSize := min(10_000, limit-total)
		refs, err := s.repo.RecoverDueCredentials(ctx, time.Now(), batchSize)
		if err != nil {
			return total, err
		}
		for _, ref := range refs {
			if ref.PoolID > 0 {
				poolIDs[ref.PoolID] = struct{}{}
			}
		}
		total += len(refs)
		if len(refs) < batchSize {
			break
		}
	}
	for poolID := range poolIDs {
		if err := s.ReconcilePool(ctx, poolID); err != nil {
			s.pendingReconcile.Store(poolID, struct{}{})
			return total, err
		}
		s.pendingReconcile.Delete(poolID)
	}
	return total, nil
}

func (s *HuggingFaceService) invalidateGroupPools(groupID int64) {
	s.poolCache.Delete(groupID)
	s.poolSF.Forget(strconv.FormatInt(groupID, 10))
}

func (s *HuggingFaceService) poolsForGroup(ctx context.Context, groupID int64) ([]HuggingFacePool, error) {
	if cached, ok := s.poolCache.Load(groupID); ok {
		entry := cached.(hfPoolCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return append([]HuggingFacePool(nil), entry.pools...), nil
		}
	}
	key := strconv.FormatInt(groupID, 10)
	value, err, _ := s.poolSF.Do(key, func() (any, error) {
		pools, err := s.repo.ListPoolsByGroup(ctx, groupID, false)
		if err != nil {
			return nil, err
		}
		active := pools[:0]
		for _, pool := range pools {
			if pool.Status == StatusActive {
				active = append(active, pool)
			}
		}
		copyPools := append([]HuggingFacePool(nil), active...)
		s.poolCache.Store(groupID, hfPoolCacheEntry{
			pools:     copyPools,
			expiresAt: time.Now().Add(time.Duration(s.cfg.PoolMetadataCacheSeconds) * time.Second),
		})
		return copyPools, nil
	})
	if err != nil {
		return nil, err
	}
	return append([]HuggingFacePool(nil), value.([]HuggingFacePool)...), nil
}

func hfModelMatches(patterns []string, model string) bool {
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	for _, pattern := range patterns {
		if wildcardMatch(pattern, model) {
			return true
		}
	}
	return false
}

// wildcardMatch implements a bounded glob with '*' and '?' and treats '/'
// like an ordinary model-name character.
func wildcardMatch(pattern, value string) bool {
	p, v := []rune(pattern), []rune(value)
	pi, vi, star, checkpoint := 0, 0, -1, 0
	for vi < len(v) {
		if pi < len(p) && (p[pi] == '?' || p[pi] == v[vi]) {
			pi++
			vi++
			continue
		}
		if pi < len(p) && p[pi] == '*' {
			star = pi
			pi++
			checkpoint = vi
			continue
		}
		if star >= 0 {
			pi = star + 1
			checkpoint++
			vi = checkpoint
			continue
		}
		return false
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

func (s *HuggingFaceService) ListModels(ctx context.Context, groupID int64) ([]string, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	pools, err := s.poolsForGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	for _, pool := range pools {
		for _, model := range pool.Models {
			if strings.ContainsAny(model, "*?") {
				continue
			}
			seen[model] = struct{}{}
		}
	}
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func (s *HuggingFaceService) orderWeightedPools(ctx context.Context, groupID int64, pools []HuggingFacePool) []HuggingFacePool {
	if len(pools) < 2 {
		return pools
	}
	selected, err := s.cache.PickWeightedPool(ctx, groupID, pools)
	if err != nil || selected <= 0 {
		sort.SliceStable(pools, func(i, j int) bool { return pools[i].ID < pools[j].ID })
		return pools
	}
	ordered := make([]HuggingFacePool, 0, len(pools))
	for _, pool := range pools {
		if pool.ID == selected {
			ordered = append(ordered, pool)
			break
		}
	}
	for _, pool := range pools {
		if pool.ID != selected {
			ordered = append(ordered, pool)
		}
	}
	return ordered
}

// CandidateAccounts returns at most CandidatePoolSize hydrated credentials and
// scans at most CandidateScanSize Redis members, regardless of a pool's total
// cardinality.
func (s *HuggingFaceService) CandidateAccounts(ctx context.Context, groupID *int64, model string, excluded map[int64]struct{}) ([]*Account, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	if groupID == nil || *groupID <= 0 {
		return nil, ErrHFPoolHasNoCandidates
	}
	pools, err := s.poolsForGroup(ctx, *groupID)
	if err != nil {
		return nil, err
	}
	matching := make([]HuggingFacePool, 0, len(pools))
	for _, pool := range pools {
		if hfModelMatches(pool.Models, model) {
			matching = append(matching, pool)
		}
	}
	if len(matching) == 0 {
		return nil, ErrHFModelNotSupported
	}
	sort.SliceStable(matching, func(i, j int) bool {
		if matching[i].Priority != matching[j].Priority {
			return matching[i].Priority < matching[j].Priority
		}
		return matching[i].ID < matching[j].ID
	})

	anyCooling := false
	for start := 0; start < len(matching); {
		end := start + 1
		for end < len(matching) && matching[end].Priority == matching[start].Priority {
			end++
		}
		level := make([]HuggingFacePool, 0, end-start)
		for _, pool := range matching[start:end] {
			remaining, cacheErr := s.cache.PoolCooldownRemaining(ctx, pool.ID)
			if cacheErr != nil {
				return nil, cacheErr
			}
			if remaining > 0 {
				anyCooling = true
				continue
			}
			level = append(level, pool)
		}
		level = s.orderWeightedPools(ctx, *groupID, level)
		accounts, err := s.candidatesFromPoolLevel(ctx, level, excluded)
		if err != nil {
			return nil, err
		}
		if len(accounts) > 0 {
			return accounts, nil
		}
		start = end
	}
	if anyCooling {
		return nil, ErrHFPoolRateLimited
	}
	return nil, ErrHFPoolHasNoCandidates
}

func (s *HuggingFaceService) candidatesFromPoolLevel(ctx context.Context, pools []HuggingFacePool, excluded map[int64]struct{}) ([]*Account, error) {
	type orderedRef struct {
		ref  HFCredentialRef
		pool HuggingFacePool
	}
	refs := make([]orderedRef, 0, s.cfg.CandidateScanSize)
	scanRemaining := s.cfg.CandidateScanSize
	for _, pool := range pools {
		if scanRemaining <= 0 {
			break
		}
		hasIndex, err := s.cache.HasPoolIndex(ctx, pool.ID)
		if err != nil {
			return nil, err
		}
		if !hasIndex {
			if err := s.ensurePoolIndex(ctx, pool.ID); err != nil {
				return nil, err
			}
		}
		priorities, err := s.cache.ListPriorities(ctx, pool.ID, scanRemaining)
		if err != nil {
			return nil, err
		}
		if len(priorities) == 0 {
			scanRemaining--
			continue
		}
		for _, priority := range priorities {
			if scanRemaining <= 0 {
				break
			}
			limit := min(scanRemaining, s.cfg.CandidatePoolSize)
			ids, err := s.cache.RotateCandidates(ctx, pool.ID, priority, limit, time.Now())
			if err != nil {
				return nil, err
			}
			// Empty/delayed priority buckets also consume one unit. This keeps
			// request work bounded even if an operator assigns 100k distinct
			// priorities and every credential is cooling down.
			scanRemaining -= max(1, len(ids))
			for _, id := range ids {
				if excluded != nil {
					if _, skip := excluded[id]; skip {
						continue
					}
				}
				refs = append(refs, orderedRef{ref: HFCredentialRef{AccountID: id, PoolID: pool.ID, Priority: priority}, pool: pool})
			}
			if len(refs) >= s.cfg.CandidatePoolSize {
				break
			}
		}
		if len(refs) >= s.cfg.CandidatePoolSize {
			break
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(refs))
	for _, item := range refs {
		ids = append(ids, item.ref.AccountID)
	}
	hydrated, err := s.accounts.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(hydrated))
	for _, account := range hydrated {
		if account != nil {
			byID[account.ID] = account
		}
	}
	out := make([]*Account, 0, min(len(refs), s.cfg.CandidatePoolSize))
	for _, item := range refs {
		account := byID[item.ref.AccountID]
		if account == nil || account.Platform != PlatformHuggingFace || account.Type != AccountTypeAPIKey || !account.IsSchedulable() || account.HuggingFacePoolID() != item.pool.ID {
			continue
		}
		ciphertext := strings.TrimSpace(account.GetCredential("api_key_ciphertext"))
		token, decryptErr := s.protector.Decrypt(ciphertext)
		if decryptErr != nil || !validHFToken(token) {
			_ = s.disableUndecryptableCredential(ctx, account, item.ref)
			continue
		}
		credentials := make(map[string]any, len(account.Credentials)+2)
		for key, value := range account.Credentials {
			if key != "api_key_ciphertext" {
				credentials[key] = value
			}
		}
		credentials["api_key"] = token
		credentials["base_url"] = item.pool.BaseURL
		account.Credentials = credentials
		if account.Extra == nil {
			account.Extra = make(map[string]any)
		}
		account.Extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceChatCompletions)
		account.Extra[openai_compat.ExtraKeyResponsesSupported] = false
		out = append(out, account)
		if len(out) == s.cfg.CandidatePoolSize {
			break
		}
	}
	return out, nil
}

func (s *HuggingFaceService) disableUndecryptableCredential(ctx context.Context, account *Account, ref HFCredentialRef) error {
	transition := HFCredentialTransition{
		Status: StatusDisabled, Schedulable: false,
		Reason: HFDisabledReasonDecryptFailed, ErrorMessage: "Hugging Face credential cannot be decrypted",
	}
	updated, applied, err := s.repo.TransitionCredential(ctx, account.ID, account.HuggingFaceFingerprint(), transition)
	if err != nil || !applied {
		return err
	}
	if updated.PoolID == 0 {
		updated = ref
	}
	if err := s.cache.RemoveCredential(ctx, updated); err != nil {
		s.pendingReconcile.Store(updated.PoolID, struct{}{})
		return err
	}
	return nil
}

func (s *HuggingFaceService) RecoverAtForMonthlyExhaustion(now time.Time) time.Time {
	location, err := time.LoadLocation(s.cfg.MonthlyRecoveryTimezone)
	if err != nil {
		location = time.UTC
	}
	local := now.In(location)
	return time.Date(local.Year(), local.Month()+1, 1, s.cfg.MonthlyRecoveryHour, 0, 0, 0, location)
}

func (s *HuggingFaceService) ObserveHTTPFailure(ctx context.Context, account *Account, status int, headers http.Header, body []byte) *UpstreamFailoverError {
	if !s.Enabled() || account == nil || account.Platform != PlatformHuggingFace {
		return nil
	}
	now := time.Now()
	upstreamStatusCode := status
	transition := HFCredentialTransition{
		Status: StatusActive, Schedulable: true, UpstreamStatusCode: &upstreamStatusCode,
	}
	retryAfter := time.Duration(0)
	clientMessage := "Hugging Face pool is temporarily unavailable"
	switch {
	case status == http.StatusUnauthorized:
		transition.Status, transition.Schedulable = StatusDisabled, false
		transition.Reason = HFDisabledReasonInvalidToken
		transition.ErrorMessage = "Hugging Face rejected the credential"
	case status == http.StatusForbidden:
		transition.Status, transition.Schedulable = StatusDisabled, false
		transition.Reason = HFDisabledReasonForbidden
		transition.ErrorMessage = "Hugging Face credential does not have permission"
	case status == http.StatusPaymentRequired && isHFMonthlyIncludedCreditsExhausted(body):
		recoverAt := s.RecoverAtForMonthlyExhaustion(now)
		transition.Status, transition.Schedulable = StatusDisabled, false
		transition.Reason = HFDisabledReasonMonthlyExhausted
		transition.ErrorMessage = "Hugging Face monthly included credits are exhausted"
		transition.RecoverAt = &recoverAt
	case status == http.StatusPaymentRequired:
		retryAfter = time.Duration(s.cfg.BillingCooldownSeconds) * time.Second
		readyAt := now.Add(retryAfter)
		transition.Reason = HFTemporaryReasonBillingRequired
		transition.ErrorMessage = "Hugging Face billing credit is temporarily unavailable"
		transition.ReadyAt = &readyAt
	case status == http.StatusTooManyRequests:
		retryAfter = s.retryAfter(headers, time.Duration(s.cfg.RateLimitCooldownSeconds)*time.Second)
		readyAt := now.Add(retryAfter)
		transition.Reason = HFTemporaryReasonRateLimited
		transition.ErrorMessage = "Hugging Face credential is rate limited"
		transition.ReadyAt = &readyAt
		clientMessage = "All Hugging Face credentials are currently rate-limited"
	case status == http.StatusRequestTimeout || status >= http.StatusInternalServerError:
		retryAfter = time.Duration(s.cfg.TransientCooldownSeconds) * time.Second
		readyAt := now.Add(retryAfter)
		transition.Reason = HFTemporaryReasonTransient
		transition.ErrorMessage = "Hugging Face upstream failed temporarily"
		transition.ReadyAt = &readyAt
		if pool, err := s.repo.GetPool(ctx, account.HuggingFacePoolID()); err == nil {
			_ = s.cache.RecordPoolFailure(ctx, *pool)
		}
	default:
		return nil
	}
	transition.ErrorMessage = huggingFaceStoredErrorMessage(status, transition.ErrorMessage, body)
	ref, applied, err := s.repo.TransitionCredential(ctx, account.ID, account.HuggingFaceFingerprint(), transition)
	if err != nil {
		logger.L().Error("persist hugging face credential failure", zap.Int64("account_id", account.ID), zap.Int("status", status), zap.Error(err))
	} else if applied {
		if transition.ReadyAt != nil {
			if cacheErr := s.cache.CooldownCredential(ctx, ref, *transition.ReadyAt); cacheErr != nil {
				s.pendingReconcile.Store(ref.PoolID, struct{}{})
			}
		} else {
			if cacheErr := s.cache.RemoveCredential(ctx, ref); cacheErr != nil {
				s.pendingReconcile.Store(ref.PoolID, struct{}{})
			}
		}
	}
	return newHFFailoverError(status, retryAfter, clientMessage)
}

func huggingFaceStoredErrorMessage(status int, fallback string, body []byte) string {
	upstreamMessage := strings.TrimSpace(sanitizeUpstreamErrorMessage(extractUpstreamErrorMessage(body)))
	if upstreamMessage == "" {
		errorValue := gjson.GetBytes(body, "error")
		if errorValue.Type == gjson.String {
			upstreamMessage = strings.TrimSpace(sanitizeUpstreamErrorMessage(errorValue.String()))
		}
	}
	if upstreamMessage == "" {
		return fallback
	}
	return fmt.Sprintf("HTTP %d: %s", status, truncateString(upstreamMessage, 512))
}

func (s *HuggingFaceService) ObserveTransportFailure(ctx context.Context, account *Account) *UpstreamFailoverError {
	if !s.Enabled() || account == nil || account.Platform != PlatformHuggingFace {
		return nil
	}
	retryAfter := time.Duration(s.cfg.TransientCooldownSeconds) * time.Second
	readyAt := time.Now().Add(retryAfter)
	transition := HFCredentialTransition{
		Status: StatusActive, Schedulable: true, Reason: HFTemporaryReasonTransient,
		ErrorMessage: "Hugging Face transport failed temporarily", ReadyAt: &readyAt,
	}
	ref, applied, err := s.repo.TransitionCredential(ctx, account.ID, account.HuggingFaceFingerprint(), transition)
	if err == nil && applied {
		if cacheErr := s.cache.CooldownCredential(ctx, ref, readyAt); cacheErr != nil {
			s.pendingReconcile.Store(ref.PoolID, struct{}{})
		}
	}
	if pool, err := s.repo.GetPool(ctx, account.HuggingFacePoolID()); err == nil {
		_ = s.cache.RecordPoolFailure(ctx, *pool)
	}
	return newHFFailoverError(http.StatusBadGateway, retryAfter, "Hugging Face pool is temporarily unavailable")
}

func (s *HuggingFaceService) ObserveSuccess(ctx context.Context, account *Account) {
	if !s.Enabled() || account == nil || account.Platform != PlatformHuggingFace {
		return
	}
	now := time.Now()
	expiredRateLimit := account.RateLimitResetAt != nil && !now.Before(*account.RateLimitResetAt)
	expiredTemporary := account.TempUnschedulableUntil != nil && !now.Before(*account.TempUnschedulableUntil)
	activeRateLimit := account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt)
	activeTemporary := account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil)
	if (expiredRateLimit || expiredTemporary) && !activeRateLimit && !activeTemporary {
		transition := HFCredentialTransition{Status: StatusActive, Schedulable: true, ClearTemporary: true}
		if _, applied, err := s.repo.TransitionCredential(ctx, account.ID, account.HuggingFaceFingerprint(), transition); err != nil {
			logger.L().Warn("clear recovered hugging face credential state", zap.Int64("account_id", account.ID), zap.Error(err))
		} else if applied {
			account.ErrorMessage = ""
			account.RateLimitedAt = nil
			account.RateLimitResetAt = nil
			account.TempUnschedulableUntil = nil
			account.TempUnschedulableReason = ""
		}
	}
	_ = s.cache.ClearPoolFailure(ctx, account.HuggingFacePoolID())
}

func (s *HuggingFaceService) retryAfter(headers http.Header, fallback time.Duration) time.Duration {
	maxDelay := time.Duration(s.cfg.MaxRetryAfterSeconds) * time.Second
	if maxDelay <= 0 {
		maxDelay = time.Hour
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return fallback
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
		return min(time.Duration(seconds)*time.Second, maxDelay)
	}
	if at, err := http.ParseTime(raw); err == nil {
		delay := time.Until(at)
		if delay < 0 {
			return fallback
		}
		return min(delay, maxDelay)
	}
	return fallback
}

func isHFMonthlyIncludedCreditsExhausted(body []byte) bool {
	text := strings.ToLower(string(body))
	return strings.Contains(text, "monthly included credits") &&
		(strings.Contains(text, "deplet") || strings.Contains(text, "exhaust") || strings.Contains(text, "exceed") || strings.Contains(text, "used up"))
}
