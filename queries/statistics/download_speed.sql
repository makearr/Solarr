-- name: InsertDownloadSpeed :exec
INSERT INTO download_speed (at, downloader_id, size_bytes, seconds) VALUES (?, ?, ?, ?);

-- Downloads, bytes and seconds per client since a time.
-- name: AggregateDownloadSpeedByClient :many
SELECT downloader_id, CAST(COUNT(*) AS INTEGER) AS downloads,
       CAST(SUM(size_bytes) AS INTEGER) AS size_bytes, CAST(SUM(seconds) AS INTEGER) AS seconds
FROM download_speed
WHERE at >= sqlc.arg(since)
GROUP BY downloader_id
ORDER BY downloader_id;

-- The oldest page of rows before a time, removed.
-- name: DeleteDownloadSpeedBefore :execrows
DELETE FROM download_speed WHERE id IN (
  SELECT old.id FROM download_speed old WHERE old.at < sqlc.arg(before) ORDER BY old.at LIMIT sqlc.arg(page_size)
);
