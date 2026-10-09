-- name: SetStatRollup :exec
INSERT INTO stat_rollup (metric, dimension, period, value, count, computed_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (metric, dimension, period) DO UPDATE SET
  value = excluded.value, count = excluded.count, computed_at = excluded.computed_at;

-- name: DeleteStatRollupMetric :exec
DELETE FROM stat_rollup WHERE metric = ?;

-- name: DeleteStatRollupMetricForCategory :exec
DELETE FROM stat_rollup WHERE metric = sqlc.arg(metric) AND dimension LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%';

-- One library's current figures of a metric.
-- name: ListStatRollupForCategory :many
SELECT metric, dimension, period, value, count, computed_at
FROM stat_rollup
WHERE metric = sqlc.arg(metric) AND period = 0 AND dimension LIKE CAST(sqlc.arg(prefix) AS TEXT) || '%'
ORDER BY dimension;

-- name: CountStatRollup :one
SELECT CAST(COUNT(*) AS INTEGER) AS figures FROM stat_rollup;
