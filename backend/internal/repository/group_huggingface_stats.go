package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// HF credentials belong to groups through pools, never account_groups. Keep this
// reporting-only join separate from ordinary account repositories and schedulers.
// Comparing pool IDs as text also avoids casting malformed credential metadata.
const hfGroupAccountsFromSQL = `FROM hf_pools p
	JOIN groups g ON g.id = p.group_id AND g.platform = 'huggingface' AND g.deleted_at IS NULL
	JOIN accounts a ON a.extra->>'hf_pool_id' = p.id::text
		AND a.platform = 'huggingface' AND a.deleted_at IS NULL
	WHERE p.group_id = ANY($1) AND p.deleted_at IS NULL`

// ListHuggingFaceCapacityByGroupIDs returns only IDs and concurrency limits;
// loading full credentials/JSON for a 100k-key pool would be unnecessarily costly.
// Availability follows the same durable state as group account counts. Runtime
// pool circuit breakers and model-specific routing are not capacity limits.
func (r *groupRepository) ListHuggingFaceCapacityByGroupIDs(ctx context.Context, groupIDs []int64) ([]service.GroupAccountCapacityRow, error) {
	if len(groupIDs) == 0 {
		return []service.GroupAccountCapacityRow{}, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT p.group_id, a.id, a.concurrency
		`+hfGroupAccountsFromSQL+`
		AND p.status = 'active' AND a.type = 'apikey' AND `+groupAccountAvailableSQL+`
		ORDER BY p.group_id, a.id`, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]service.GroupAccountCapacityRow, 0)
	for rows.Next() {
		var row service.GroupAccountCapacityRow
		if err := rows.Scan(&row.GroupID, &row.AccountID, &row.Concurrency); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, rows.Close()
}
