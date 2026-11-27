-- name: UpsertMetricPoint :exec
INSERT INTO metric_point (resolution, at, series, scope, value) VALUES (?, ?, ?, ?, ?)
ON CONFLICT (series, scope, resolution, at) DO UPDATE SET value = excluded.value;

-- name: ListMetricPoints :many
SELECT resolution, at, series, scope, value FROM metric_point
WHERE series = sqlc.arg(series) AND scope = sqlc.arg(scope) AND resolution = sqlc.arg(resolution) AND at >= sqlc.arg(since)
ORDER BY at;

-- name: ListMetricScopes :many
SELECT DISTINCT series, scope FROM metric_point ORDER BY series, scope;

-- Averages the minutes of finished hours into hour rows.
-- name: RollUpMetricMinutes :exec
INSERT INTO metric_point (resolution, at, series, scope, value)
SELECT 3600, CAST(m.at / 3600 AS INTEGER) * 3600, m.series, m.scope, CAST(AVG(m.value) AS REAL)
FROM metric_point AS m WHERE m.resolution = 60 AND m.at >= sqlc.arg(since) AND m.at < sqlc.arg(before)
GROUP BY m.series, m.scope, CAST(m.at / 3600 AS INTEGER)
ON CONFLICT (series, scope, resolution, at) DO UPDATE SET value = excluded.value;

-- name: DeleteMetricPointsBefore :execrows
DELETE FROM metric_point WHERE resolution = sqlc.arg(resolution) AND at < sqlc.arg(before);
