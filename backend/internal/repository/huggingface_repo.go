package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type huggingFaceRepository struct {
	db *sql.DB
}

func NewHuggingFaceRepository(db *sql.DB) service.HuggingFaceRepository {
	return &huggingFaceRepository{db: db}
}

func scanHFPool(scanner interface{ Scan(...any) error }, pool *service.HuggingFacePool) error {
	var modelsJSON []byte
	if err := scanner.Scan(
		&pool.ID, &pool.GroupID, &pool.Name, &pool.BaseURL, &pool.Priority,
		&pool.Weight, &pool.Status, &modelsJSON, &pool.FailureThreshold,
		&pool.CircuitCooldownSeconds, &pool.CreatedAt, &pool.UpdatedAt,
	); err != nil {
		return err
	}
	if err := json.Unmarshal(modelsJSON, &pool.Models); err != nil {
		return fmt.Errorf("decode huggingface pool models: %w", err)
	}
	return nil
}

const hfPoolColumns = `id, group_id, name, base_url, priority, weight, status, models,
failure_threshold, circuit_cooldown_seconds, created_at, updated_at`

const hfPoolColumnsPrefixed = `p.id, p.group_id, p.name, p.base_url, p.priority, p.weight, p.status, p.models,
p.failure_threshold, p.circuit_cooldown_seconds, p.created_at, p.updated_at`

func (r *huggingFaceRepository) CreatePool(ctx context.Context, pool *service.HuggingFacePool) error {
	if r == nil || r.db == nil || pool == nil {
		return errors.New("invalid huggingface pool repository input")
	}
	models, err := json.Marshal(pool.Models)
	if err != nil {
		return err
	}
	row := r.db.QueryRowContext(ctx, `
		INSERT INTO hf_pools
			(group_id, name, base_url, priority, weight, status, models, failure_threshold, circuit_cooldown_seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)
		RETURNING `+hfPoolColumns,
		pool.GroupID, pool.Name, pool.BaseURL, pool.Priority, pool.Weight, pool.Status,
		string(models), pool.FailureThreshold, pool.CircuitCooldownSeconds,
	)
	return scanHFPool(row, pool)
}

func (r *huggingFaceRepository) UpdatePool(ctx context.Context, pool *service.HuggingFacePool) error {
	if r == nil || r.db == nil || pool == nil {
		return errors.New("invalid huggingface pool repository input")
	}
	models, err := json.Marshal(pool.Models)
	if err != nil {
		return err
	}
	row := r.db.QueryRowContext(ctx, `
		UPDATE hf_pools SET
			name=$2, base_url=$3, priority=$4, weight=$5, status=$6, models=$7::jsonb,
			failure_threshold=$8, circuit_cooldown_seconds=$9, updated_at=NOW()
		WHERE id=$1 AND deleted_at IS NULL
		RETURNING `+hfPoolColumns,
		pool.ID, pool.Name, pool.BaseURL, pool.Priority, pool.Weight, pool.Status,
		string(models), pool.FailureThreshold, pool.CircuitCooldownSeconds,
	)
	if err := scanHFPool(row, pool); errors.Is(err, sql.ErrNoRows) {
		return service.ErrHFPoolNotFound
	} else {
		return err
	}
}

func (r *huggingFaceRepository) GetPool(ctx context.Context, id int64) (*service.HuggingFacePool, error) {
	if r == nil || r.db == nil || id <= 0 {
		return nil, service.ErrHFPoolNotFound
	}
	pool := &service.HuggingFacePool{}
	err := scanHFPool(r.db.QueryRowContext(ctx, `SELECT `+hfPoolColumns+` FROM hf_pools WHERE id=$1 AND deleted_at IS NULL`, id), pool)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrHFPoolNotFound
	}
	return pool, err
}

func (r *huggingFaceRepository) ListPoolsByGroup(ctx context.Context, groupID int64, withStats bool) ([]service.HuggingFacePool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("huggingface repository is unavailable")
	}
	query := `SELECT ` + hfPoolColumns
	if withStats {
		query = `SELECT ` + hfPoolColumnsPrefixed + `,
			COUNT(a.id) AS credential_count,
			COUNT(a.id) FILTER (WHERE a.status='active' AND a.schedulable
				AND (a.rate_limit_reset_at IS NULL OR a.rate_limit_reset_at <= NOW())
				AND (a.temp_unschedulable_until IS NULL OR a.temp_unschedulable_until <= NOW())) AS available_count,
			COUNT(a.id) FILTER (WHERE a.status='active' AND a.schedulable AND
				((a.rate_limit_reset_at IS NOT NULL AND a.rate_limit_reset_at > NOW()) OR
				 (a.temp_unschedulable_until IS NOT NULL AND a.temp_unschedulable_until > NOW()))) AS cooldown_count,
			COUNT(a.id) FILTER (WHERE a.status <> 'active' OR NOT a.schedulable) AS disabled_count
		FROM hf_pools p
		LEFT JOIN accounts a ON a.platform='huggingface' AND a.deleted_at IS NULL
			AND a.extra->>'hf_pool_id'=p.id::text
		WHERE p.group_id=$1 AND p.deleted_at IS NULL
		GROUP BY p.id
		ORDER BY p.priority, p.id`
	} else {
		query += ` FROM hf_pools WHERE group_id=$1 AND deleted_at IS NULL ORDER BY priority, id`
	}
	rows, err := r.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pools := make([]service.HuggingFacePool, 0)
	for rows.Next() {
		pool := service.HuggingFacePool{}
		var scanErr error
		if withStats {
			var modelsJSON []byte
			scanErr = rows.Scan(
				&pool.ID, &pool.GroupID, &pool.Name, &pool.BaseURL, &pool.Priority,
				&pool.Weight, &pool.Status, &modelsJSON, &pool.FailureThreshold,
				&pool.CircuitCooldownSeconds, &pool.CreatedAt, &pool.UpdatedAt,
				&pool.CredentialCount, &pool.AvailableCount, &pool.CooldownCount, &pool.DisabledCount,
			)
			if scanErr == nil {
				scanErr = json.Unmarshal(modelsJSON, &pool.Models)
			}
		} else {
			scanErr = scanHFPool(rows, &pool)
		}
		if scanErr != nil {
			return nil, scanErr
		}
		pools = append(pools, pool)
	}
	return pools, rows.Err()
}

func (r *huggingFaceRepository) ListActivePools(ctx context.Context) ([]service.HuggingFacePool, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("huggingface repository is unavailable")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+hfPoolColumns+` FROM hf_pools WHERE deleted_at IS NULL AND status='active' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pools := make([]service.HuggingFacePool, 0)
	for rows.Next() {
		pool := service.HuggingFacePool{}
		if err := scanHFPool(rows, &pool); err != nil {
			return nil, err
		}
		pools = append(pools, pool)
	}
	return pools, rows.Err()
}

func (r *huggingFaceRepository) DeletePool(ctx context.Context, id int64) error {
	if r == nil || r.db == nil {
		return errors.New("huggingface repository is unavailable")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE hf_pools SET status='disabled', deleted_at=NOW(), updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return service.ErrHFPoolNotFound
	}
	_, err = tx.ExecContext(ctx, `
		UPDATE accounts SET status='disabled', schedulable=false, deleted_at=NOW(), updated_at=NOW()
		WHERE platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$1`, strconv.FormatInt(id, 10))
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (r *huggingFaceRepository) ImportCredentials(ctx context.Context, poolID int64, credentials []service.HFProtectedCredential) ([]service.HFCredentialRef, int, error) {
	if r == nil || r.db == nil {
		return nil, 0, errors.New("huggingface repository is unavailable")
	}
	if len(credentials) == 0 {
		return nil, 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var poolExists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hf_pools WHERE id=$1 AND deleted_at IS NULL)`, poolID).Scan(&poolExists); err != nil {
		return nil, 0, err
	}
	if !poolExists {
		return nil, 0, service.ErrHFPoolNotFound
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE hf_import_stage (
		fingerprint TEXT PRIMARY KEY, ciphertext TEXT NOT NULL, suffix TEXT NOT NULL,
		priority INTEGER NOT NULL, concurrency INTEGER NOT NULL
	) ON COMMIT DROP`); err != nil {
		return nil, 0, err
	}
	stmt, err := tx.PrepareContext(ctx, pq.CopyIn("hf_import_stage", "fingerprint", "ciphertext", "suffix", "priority", "concurrency"))
	if err != nil {
		return nil, 0, err
	}
	for _, credential := range credentials {
		if _, err := stmt.ExecContext(ctx, credential.Fingerprint, credential.Ciphertext, credential.Suffix, credential.Priority, credential.Concurrency); err != nil {
			_ = stmt.Close()
			return nil, 0, err
		}
	}
	if _, err := stmt.ExecContext(ctx); err != nil {
		_ = stmt.Close()
		return nil, 0, err
	}
	if err := stmt.Close(); err != nil {
		return nil, 0, err
	}
	rows, err := tx.QueryContext(ctx, `
		INSERT INTO accounts
			(name, platform, type, credentials, extra, concurrency, priority, status,
			 schedulable, auto_pause_on_expired, quota_dimension, created_at, updated_at)
		SELECT
			'hf-' || $1::text || '-' || LEFT(s.fingerprint, 12),
			'huggingface', 'apikey',
			jsonb_build_object('api_key_ciphertext', s.ciphertext),
			jsonb_build_object(
				'hf_pool_id', $1::text,
				'hf_token_fingerprint', s.fingerprint,
				'hf_token_suffix', s.suffix,
				'openai_responses_mode', 'force_chat_completions',
				'openai_responses_supported', false
			),
			s.concurrency, s.priority, 'active', true, true, 'global', NOW(), NOW()
		FROM hf_import_stage s
		ON CONFLICT ((extra ->> 'hf_token_fingerprint'))
			WHERE deleted_at IS NULL AND platform='huggingface' AND (extra ->> 'hf_token_fingerprint') IS NOT NULL
		DO NOTHING
		RETURNING id, priority`, poolID)
	if err != nil {
		return nil, 0, err
	}
	refs := make([]service.HFCredentialRef, 0, len(credentials))
	for rows.Next() {
		ref := service.HFCredentialRef{PoolID: poolID}
		if err := rows.Scan(&ref.AccountID, &ref.Priority); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return refs, len(credentials) - len(refs), nil
}

func (r *huggingFaceRepository) ListCredentialRefs(ctx context.Context, poolID int64) ([]service.HFCredentialRef, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, priority,
			CASE
				WHEN rate_limit_reset_at > NOW() AND temp_unschedulable_until > NOW() THEN GREATEST(rate_limit_reset_at, temp_unschedulable_until)
				WHEN rate_limit_reset_at > NOW() THEN rate_limit_reset_at
				WHEN temp_unschedulable_until > NOW() THEN temp_unschedulable_until
				ELSE NULL
			END AS ready_at
		FROM accounts
		WHERE platform='huggingface' AND deleted_at IS NULL AND status='active' AND schedulable
			AND extra->>'hf_pool_id'=$1
		ORDER BY priority, id`, strconv.FormatInt(poolID, 10))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]service.HFCredentialRef, 0)
	for rows.Next() {
		ref := service.HFCredentialRef{PoolID: poolID}
		var ready sql.NullTime
		if err := rows.Scan(&ref.AccountID, &ref.Priority, &ready); err != nil {
			return nil, err
		}
		if ready.Valid {
			at := ready.Time
			ref.ReadyAt = &at
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r *huggingFaceRepository) ListCredentials(ctx context.Context, poolID int64, limit, offset int) (*service.HFCredentialPage, error) {
	page := &service.HFCredentialPage{Items: make([]service.HFCredentialListItem, 0), Limit: limit, Offset: offset}
	poolText := strconv.FormatInt(poolID, 10)
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$1`, poolText).Scan(&page.Total); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, COALESCE(extra->>'hf_token_suffix',''), priority, concurrency,
			status, schedulable, COALESCE(extra->>'hf_disabled_reason',temp_unschedulable_reason,''), COALESCE(error_message,''),
			CASE
				WHEN COALESCE(extra->>'hf_upstream_status_code','') ~ '^[1-5][0-9]{2}$'
					THEN (extra->>'hf_upstream_status_code')::integer
				WHEN COALESCE(error_message,'') ~ '\([1-5][0-9]{2}\)'
					THEN substring(error_message FROM '\(([1-5][0-9]{2})\)')::integer
				ELSE NULL
			END,
			rate_limit_reset_at, temp_unschedulable_until,
			NULLIF(extra->>'hf_recover_at','')::timestamptz, last_used_at, created_at
		FROM accounts
		WHERE platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$1
		ORDER BY priority, id LIMIT $2 OFFSET $3`, poolText, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		item := service.HFCredentialListItem{PoolID: poolID}
		var upstreamStatusCode sql.NullInt64
		if err := rows.Scan(
			&item.AccountID, &item.Name, &item.TokenSuffix, &item.Priority, &item.Concurrency,
			&item.Status, &item.Schedulable, &item.DisabledReason, &item.ErrorMessage,
			&upstreamStatusCode,
			&item.RateLimitResetAt, &item.TempUnschedulableUntil, &item.RecoverAt,
			&item.LastUsedAt, &item.CreatedAt,
		); err != nil {
			return nil, err
		}
		if upstreamStatusCode.Valid {
			value := int(upstreamStatusCode.Int64)
			item.UpstreamStatusCode = &value
		}
		page.Items = append(page.Items, item)
	}
	return page, rows.Err()
}

const hfTimestampLayout = "2006-01-02T15:04:05.000000000Z07:00"

func formatHFTimestamp(value time.Time) string {
	return value.UTC().Format(hfTimestampLayout)
}

func nullableTimeString(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatHFTimestamp(*value)
}

func nullableInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func (r *huggingFaceRepository) TransitionCredential(ctx context.Context, accountID int64, fingerprint string, transition service.HFCredentialTransition) (service.HFCredentialRef, bool, error) {
	ref := service.HFCredentialRef{}
	if r == nil || r.db == nil || accountID <= 0 || fingerprint == "" {
		return ref, false, nil
	}
	row := r.db.QueryRowContext(ctx, `
		UPDATE accounts SET
			status=$3::varchar, schedulable=$4::boolean, error_message=NULLIF($5::text,''),
			rate_limited_at=CASE WHEN $6::text='rate_limited' THEN NOW() ELSE NULL END,
			rate_limit_reset_at=CASE WHEN $6::text='rate_limited' THEN $7::timestamptz ELSE NULL END,
			temp_unschedulable_until=CASE WHEN $7::timestamptz IS NOT NULL AND $6::text<>'rate_limited' THEN $7::timestamptz ELSE NULL END,
			temp_unschedulable_reason=CASE WHEN $7::timestamptz IS NOT NULL AND $6::text<>'rate_limited' THEN $6::text ELSE NULL END,
			extra=(CASE WHEN $3::text='disabled'
				THEN (extra - 'hf_recover_at' - 'hf_upstream_status_code') || jsonb_build_object('hf_disabled_reason',$6::text)
				ELSE extra - 'hf_disabled_reason' - 'hf_recover_at' - 'hf_upstream_status_code'
			END)
			|| CASE WHEN $8::text IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('hf_recover_at',$8::text) END
			|| CASE WHEN $10::integer IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('hf_upstream_status_code',$10::integer) END,
			updated_at=NOW()
		WHERE id=$1 AND platform='huggingface' AND deleted_at IS NULL
			AND extra->>'hf_token_fingerprint'=$2
			AND (NOT $9::boolean OR (
				status='active' AND schedulable=true
				AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at <= NOW())
				AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until <= NOW())
			))
		RETURNING id, (extra->>'hf_pool_id'), priority`,
		accountID, fingerprint, transition.Status, transition.Schedulable,
		transition.ErrorMessage, transition.Reason, nullableTimeString(transition.ReadyAt), nullableTimeString(transition.RecoverAt), transition.ClearTemporary,
		nullableInt(transition.UpstreamStatusCode),
	)
	var poolText string
	if err := row.Scan(&ref.AccountID, &poolText, &ref.Priority); errors.Is(err, sql.ErrNoRows) {
		return ref, false, nil
	} else if err != nil {
		return ref, false, err
	}
	ref.PoolID, _ = strconv.ParseInt(poolText, 10, 64)
	ref.ReadyAt = transition.ReadyAt
	return ref, true, nil
}

func (r *huggingFaceRepository) RecoverDueCredentials(ctx context.Context, now time.Time, limit int) ([]service.HFCredentialRef, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH due AS (
			SELECT a.id FROM accounts a
			JOIN hf_pools p ON p.id::text=a.extra->>'hf_pool_id' AND p.deleted_at IS NULL AND p.status='active'
			WHERE a.platform='huggingface' AND a.deleted_at IS NULL AND a.status='disabled'
				AND a.extra->>'hf_disabled_reason'='monthly_included_credits_exhausted'
				AND a.extra->>'hf_recover_at' IS NOT NULL
				AND a.extra->>'hf_recover_at' <= $1
			ORDER BY a.extra->>'hf_recover_at', a.id
			FOR UPDATE OF a SKIP LOCKED LIMIT $2
		)
		UPDATE accounts a SET status='active', schedulable=true, error_message=NULL,
			rate_limited_at=NULL, rate_limit_reset_at=NULL,
			temp_unschedulable_until=NULL, temp_unschedulable_reason=NULL,
			extra=(a.extra - 'hf_disabled_reason' - 'hf_recover_at' - 'hf_upstream_status_code'), updated_at=NOW()
		FROM due WHERE a.id=due.id
		RETURNING a.id, a.extra->>'hf_pool_id', a.priority`, formatHFTimestamp(now), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := make([]service.HFCredentialRef, 0)
	for rows.Next() {
		ref := service.HFCredentialRef{}
		var poolText string
		if err := rows.Scan(&ref.AccountID, &poolText, &ref.Priority); err != nil {
			return nil, err
		}
		ref.PoolID, _ = strconv.ParseInt(poolText, 10, 64)
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

func (r *huggingFaceRepository) RecoverCredential(ctx context.Context, poolID, accountID int64) (service.HFCredentialRef, error) {
	ref := service.HFCredentialRef{PoolID: poolID}
	err := r.db.QueryRowContext(ctx, `
		UPDATE accounts SET status='active', schedulable=true, error_message=NULL,
			rate_limited_at=NULL, rate_limit_reset_at=NULL,
			temp_unschedulable_until=NULL, temp_unschedulable_reason=NULL,
			extra=(extra - 'hf_disabled_reason' - 'hf_recover_at' - 'hf_upstream_status_code'), updated_at=NOW()
		WHERE id=$1 AND platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$2
		RETURNING id, priority`, accountID, strconv.FormatInt(poolID, 10)).Scan(&ref.AccountID, &ref.Priority)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, service.ErrAccountNotFound
	}
	return ref, err
}

func (r *huggingFaceRepository) DeleteCredential(ctx context.Context, poolID, accountID int64) (service.HFCredentialRef, error) {
	ref := service.HFCredentialRef{PoolID: poolID}
	err := r.db.QueryRowContext(ctx, `
		UPDATE accounts SET status='disabled', schedulable=false, deleted_at=NOW(), updated_at=NOW()
		WHERE id=$1 AND platform='huggingface' AND deleted_at IS NULL AND extra->>'hf_pool_id'=$2
		RETURNING id, priority`, accountID, strconv.FormatInt(poolID, 10)).Scan(&ref.AccountID, &ref.Priority)
	if errors.Is(err, sql.ErrNoRows) {
		return ref, service.ErrAccountNotFound
	}
	return ref, err
}
