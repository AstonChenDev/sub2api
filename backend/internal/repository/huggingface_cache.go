package repository

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const hfRedisPrefix = "sub2api:hf:v1"

type huggingFaceCache struct {
	rdb *redis.Client
}

func NewHuggingFaceCache(rdb *redis.Client) service.HuggingFaceCache {
	return &huggingFaceCache{rdb: rdb}
}

func hfPoolPrefix(poolID int64) string {
	return fmt.Sprintf("%s:pool:%d", hfRedisPrefix, poolID)
}

func hfGenerationKey(poolID int64) string { return hfPoolPrefix(poolID) + ":generation" }
func hfLockKey(poolID int64) string       { return hfPoolPrefix(poolID) + ":mutation-lock" }
func hfPrioritiesKey(poolID int64, generation string) string {
	return fmt.Sprintf("%s:g:%s:priorities", hfPoolPrefix(poolID), generation)
}
func hfReadyKey(poolID int64, generation string, priority int) string {
	return fmt.Sprintf("%s:g:%s:p:%d:ready", hfPoolPrefix(poolID), generation, priority)
}
func hfDelayedKey(poolID int64, generation string, priority int) string {
	return fmt.Sprintf("%s:g:%s:p:%d:delayed", hfPoolPrefix(poolID), generation, priority)
}
func hfSequenceKey(poolID int64, generation string, priority int) string {
	return fmt.Sprintf("%s:g:%s:p:%d:sequence", hfPoolPrefix(poolID), generation, priority)
}

var hfUnlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (c *huggingFaceCache) withPoolLock(ctx context.Context, poolID int64, fn func(context.Context) error) error {
	if c == nil || c.rdb == nil {
		return errors.New("huggingface cache is unavailable")
	}
	token := uuid.NewString()
	key := hfLockKey(poolID)
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		acquired, err := c.rdb.SetNX(ctx, key, token, 10*time.Minute).Result()
		if err != nil {
			return err
		}
		if acquired {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	defer func() { _, _ = hfUnlockScript.Run(context.Background(), c.rdb, []string{key}, token).Result() }()
	return fn(ctx)
}

func (c *huggingFaceCache) HasPoolIndex(ctx context.Context, poolID int64) (bool, error) {
	count, err := c.rdb.Exists(ctx, hfGenerationKey(poolID)).Result()
	return count > 0, err
}

func (c *huggingFaceCache) RebuildPoolIndex(ctx context.Context, poolID int64, loader func(context.Context) ([]service.HFCredentialRef, error)) error {
	if loader == nil {
		return errors.New("huggingface index loader is required")
	}
	return c.withPoolLock(ctx, poolID, func(lockCtx context.Context) error {
		refs, err := loader(lockCtx)
		if err != nil {
			return err
		}
		oldGeneration, err := c.rdb.Get(lockCtx, hfGenerationKey(poolID)).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return err
		}
		oldPriorities := make([]string, 0)
		if oldGeneration != "" {
			oldPriorities, _ = c.rdb.ZRange(lockCtx, hfPrioritiesKey(poolID, oldGeneration), 0, -1).Result()
		}

		generation := uuid.NewString()
		byPriority := make(map[int][]service.HFCredentialRef)
		priorities := make([]int, 0)
		for _, ref := range refs {
			if ref.AccountID <= 0 || ref.PoolID != poolID {
				continue
			}
			if _, exists := byPriority[ref.Priority]; !exists {
				priorities = append(priorities, ref.Priority)
			}
			byPriority[ref.Priority] = append(byPriority[ref.Priority], ref)
		}
		sort.Ints(priorities)
		pipe := c.rdb.Pipeline()
		createdKeys := make([]string, 0, len(priorities)*4+1)
		for _, priority := range priorities {
			readyKey := hfReadyKey(poolID, generation, priority)
			delayedKey := hfDelayedKey(poolID, generation, priority)
			sequenceKey := hfSequenceKey(poolID, generation, priority)
			createdKeys = append(createdKeys, readyKey, delayedKey, sequenceKey)
			pipe.ZAdd(lockCtx, hfPrioritiesKey(poolID, generation), redis.Z{Score: float64(priority), Member: strconv.Itoa(priority)})
			var sequence int64
			readyMembers := make([]redis.Z, 0, 1000)
			delayedMembers := make([]redis.Z, 0, 1000)
			flush := func() {
				if len(readyMembers) > 0 {
					pipe.ZAdd(lockCtx, readyKey, readyMembers...)
					readyMembers = readyMembers[:0]
				}
				if len(delayedMembers) > 0 {
					pipe.ZAdd(lockCtx, delayedKey, delayedMembers...)
					delayedMembers = delayedMembers[:0]
				}
			}
			for _, ref := range byPriority[priority] {
				member := strconv.FormatInt(ref.AccountID, 10)
				if ref.ReadyAt != nil && ref.ReadyAt.After(time.Now()) {
					delayedMembers = append(delayedMembers, redis.Z{Score: float64(ref.ReadyAt.UnixMilli()), Member: member})
				} else {
					sequence++
					readyMembers = append(readyMembers, redis.Z{Score: float64(sequence), Member: member})
				}
				if len(readyMembers)+len(delayedMembers) >= 1000 {
					flush()
				}
			}
			flush()
			pipe.Set(lockCtx, sequenceKey, sequence, 0)
		}
		createdKeys = append(createdKeys, hfPrioritiesKey(poolID, generation))
		if _, err := pipe.Exec(lockCtx); err != nil {
			if len(createdKeys) > 0 {
				_ = c.rdb.Del(context.Background(), createdKeys...).Err()
			}
			return err
		}
		if err := c.rdb.Set(lockCtx, hfGenerationKey(poolID), generation, 0).Err(); err != nil {
			_ = c.rdb.Del(context.Background(), createdKeys...).Err()
			return err
		}
		if oldGeneration != "" && oldGeneration != generation {
			oldKeys := []string{hfPrioritiesKey(poolID, oldGeneration)}
			for _, rawPriority := range oldPriorities {
				priority, parseErr := strconv.Atoi(rawPriority)
				if parseErr != nil {
					continue
				}
				oldKeys = append(oldKeys,
					hfReadyKey(poolID, oldGeneration, priority),
					hfDelayedKey(poolID, oldGeneration, priority),
					hfSequenceKey(poolID, oldGeneration, priority),
				)
			}
			cleanup := c.rdb.Pipeline()
			for _, oldKey := range oldKeys {
				cleanup.Expire(lockCtx, oldKey, 5*time.Minute)
			}
			_, _ = cleanup.Exec(lockCtx)
		}
		return nil
	})
}

func (c *huggingFaceCache) ListPriorities(ctx context.Context, poolID int64, limit int) ([]int, error) {
	if limit <= 0 {
		return nil, nil
	}
	generation, err := c.rdb.Get(ctx, hfGenerationKey(poolID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	values, err := c.rdb.ZRange(ctx, hfPrioritiesKey(poolID, generation), 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	priorities := make([]int, 0, len(values))
	for _, value := range values {
		if priority, parseErr := strconv.Atoi(value); parseErr == nil {
			priorities = append(priorities, priority)
		}
	}
	return priorities, nil
}

var hfRotateScript = redis.NewScript(`
local due = redis.call('ZRANGEBYSCORE', KEYS[2], '-inf', ARGV[1], 'LIMIT', 0, ARGV[2])
if #due > 0 then
  redis.call('ZREM', KEYS[2], unpack(due))
  local seq = redis.call('INCRBY', KEYS[3], #due)
  for i, member in ipairs(due) do
    redis.call('ZADD', KEYS[1], seq - #due + i, member)
  end
end
local popped = redis.call('ZPOPMIN', KEYS[1], ARGV[2])
if #popped == 0 then
  return {}
end
local count = #popped / 2
local seq = redis.call('INCRBY', KEYS[3], count)
local result = {}
for i = 1, #popped, 2 do
  local member = popped[i]
  table.insert(result, member)
  redis.call('ZADD', KEYS[1], seq - count + ((i + 1) / 2), member)
end
return result
`)

func (c *huggingFaceCache) RotateCandidates(ctx context.Context, poolID int64, priority, limit int, now time.Time) ([]int64, error) {
	if limit <= 0 {
		return nil, nil
	}
	generation, err := c.rdb.Get(ctx, hfGenerationKey(poolID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := hfRotateScript.Run(ctx, c.rdb, []string{
		hfReadyKey(poolID, generation, priority),
		hfDelayedKey(poolID, generation, priority),
		hfSequenceKey(poolID, generation, priority),
	}, now.UnixMilli(), limit).StringSlice()
	if err != nil && !errors.Is(err, redis.Nil) {
		return nil, err
	}
	ids := make([]int64, 0, len(result))
	for _, value := range result {
		if id, parseErr := strconv.ParseInt(value, 10, 64); parseErr == nil {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (c *huggingFaceCache) currentGenerationLocked(ctx context.Context, poolID int64) (string, error) {
	generation, err := c.rdb.Get(ctx, hfGenerationKey(poolID)).Result()
	if errors.Is(err, redis.Nil) {
		generation = uuid.NewString()
		if err := c.rdb.Set(ctx, hfGenerationKey(poolID), generation, 0).Err(); err != nil {
			return "", err
		}
		return generation, nil
	}
	return generation, err
}

func (c *huggingFaceCache) AddCredential(ctx context.Context, ref service.HFCredentialRef) error {
	return c.withPoolLock(ctx, ref.PoolID, func(lockCtx context.Context) error {
		generation, err := c.currentGenerationLocked(lockCtx, ref.PoolID)
		if err != nil {
			return err
		}
		priorityMember := strconv.Itoa(ref.Priority)
		member := strconv.FormatInt(ref.AccountID, 10)
		pipe := c.rdb.TxPipeline()
		pipe.ZAdd(lockCtx, hfPrioritiesKey(ref.PoolID, generation), redis.Z{Score: float64(ref.Priority), Member: priorityMember})
		pipe.ZRem(lockCtx, hfReadyKey(ref.PoolID, generation, ref.Priority), member)
		pipe.ZRem(lockCtx, hfDelayedKey(ref.PoolID, generation, ref.Priority), member)
		if ref.ReadyAt != nil && ref.ReadyAt.After(time.Now()) {
			pipe.ZAdd(lockCtx, hfDelayedKey(ref.PoolID, generation, ref.Priority), redis.Z{Score: float64(ref.ReadyAt.UnixMilli()), Member: member})
			_, err = pipe.Exec(lockCtx)
			return err
		}
		sequence, err := c.rdb.Incr(lockCtx, hfSequenceKey(ref.PoolID, generation, ref.Priority)).Result()
		if err != nil {
			return err
		}
		pipe.ZAdd(lockCtx, hfReadyKey(ref.PoolID, generation, ref.Priority), redis.Z{Score: float64(sequence), Member: member})
		_, err = pipe.Exec(lockCtx)
		return err
	})
}

var hfRemoveCredentialScript = redis.NewScript(`
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
if redis.call('ZCARD', KEYS[1]) == 0 and redis.call('ZCARD', KEYS[2]) == 0 then
  redis.call('ZREM', KEYS[3], ARGV[2])
end
return 1
`)

func (c *huggingFaceCache) RemoveCredential(ctx context.Context, ref service.HFCredentialRef) error {
	return c.withPoolLock(ctx, ref.PoolID, func(lockCtx context.Context) error {
		generation, err := c.rdb.Get(lockCtx, hfGenerationKey(ref.PoolID)).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		member := strconv.FormatInt(ref.AccountID, 10)
		return hfRemoveCredentialScript.Run(lockCtx, c.rdb, []string{
			hfReadyKey(ref.PoolID, generation, ref.Priority),
			hfDelayedKey(ref.PoolID, generation, ref.Priority),
			hfPrioritiesKey(ref.PoolID, generation),
		}, member, strconv.Itoa(ref.Priority)).Err()
	})
}

func (c *huggingFaceCache) CooldownCredential(ctx context.Context, ref service.HFCredentialRef, readyAt time.Time) error {
	return c.withPoolLock(ctx, ref.PoolID, func(lockCtx context.Context) error {
		generation, err := c.rdb.Get(lockCtx, hfGenerationKey(ref.PoolID)).Result()
		if errors.Is(err, redis.Nil) {
			return nil
		}
		if err != nil {
			return err
		}
		member := strconv.FormatInt(ref.AccountID, 10)
		pipe := c.rdb.TxPipeline()
		pipe.ZRem(lockCtx, hfReadyKey(ref.PoolID, generation, ref.Priority), member)
		pipe.ZAdd(lockCtx, hfDelayedKey(ref.PoolID, generation, ref.Priority), redis.Z{Score: float64(readyAt.UnixMilli()), Member: member})
		_, err = pipe.Exec(lockCtx)
		return err
	})
}

var hfSmoothWeightedScript = redis.NewScript(`
local total = 0
local selected = nil
local selectedCurrent = nil
for i = 1, #ARGV, 2 do
  local id = ARGV[i]
  local weight = tonumber(ARGV[i + 1])
  total = total + weight
  local current = tonumber(redis.call('HGET', KEYS[1], id) or '0') + weight
  redis.call('HSET', KEYS[1], id, current)
  if selected == nil or current > selectedCurrent or (current == selectedCurrent and tonumber(id) < tonumber(selected)) then
    selected = id
    selectedCurrent = current
  end
end
if selected == nil then return nil end
redis.call('HINCRBY', KEYS[1], selected, -total)
redis.call('EXPIRE', KEYS[1], 86400)
return selected
`)

func (c *huggingFaceCache) PickWeightedPool(ctx context.Context, groupID int64, pools []service.HuggingFacePool) (int64, error) {
	if len(pools) == 0 {
		return 0, nil
	}
	args := make([]any, 0, len(pools)*2)
	for _, pool := range pools {
		args = append(args, pool.ID, max(1, pool.Weight))
	}
	key := fmt.Sprintf("%s:group:%d:smooth-weight", hfRedisPrefix, groupID)
	return hfSmoothWeightedScript.Run(ctx, c.rdb, []string{key}, args...).Int64()
}

func (c *huggingFaceCache) PoolCooldownRemaining(ctx context.Context, poolID int64) (time.Duration, error) {
	result, err := c.rdb.PTTL(ctx, hfPoolPrefix(poolID)+":circuit-open").Result()
	if err != nil {
		return 0, err
	}
	if result < 0 {
		return 0, nil
	}
	return result, nil
}

var hfRecordPoolFailureScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], math.max(60, tonumber(ARGV[2]) * 2))
if count >= tonumber(ARGV[1]) then
  redis.call('SET', KEYS[2], '1', 'EX', ARGV[2])
  redis.call('DEL', KEYS[1])
end
return count
`)

func (c *huggingFaceCache) RecordPoolFailure(ctx context.Context, pool service.HuggingFacePool) error {
	base := hfPoolPrefix(pool.ID)
	return hfRecordPoolFailureScript.Run(ctx, c.rdb,
		[]string{base + ":failure-count", base + ":circuit-open"},
		max(1, pool.FailureThreshold), max(1, pool.CircuitCooldownSeconds),
	).Err()
}

func (c *huggingFaceCache) ClearPoolFailure(ctx context.Context, poolID int64) error {
	base := hfPoolPrefix(poolID)
	return c.rdb.Del(ctx, base+":failure-count", base+":circuit-open").Err()
}
