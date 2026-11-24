-- Conversion savings, added to as files finish (a restore adds the negative of what it undoes).

-- name: AddTranscodeStat :exec
INSERT INTO transcode_stat (category_id, day, kind, files, original_bytes, result_bytes, encode_seconds, failed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (category_id, day, kind) DO UPDATE SET
  files = transcode_stat.files + excluded.files,
  original_bytes = transcode_stat.original_bytes + excluded.original_bytes,
  result_bytes = transcode_stat.result_bytes + excluded.result_bytes,
  encode_seconds = transcode_stat.encode_seconds + excluded.encode_seconds,
  failed = transcode_stat.failed + excluded.failed;

-- name: ListTranscodeStatTotals :many
SELECT category_id, kind, CAST(SUM(files) AS INTEGER) AS files, CAST(SUM(original_bytes) AS INTEGER) AS original_bytes,
       CAST(SUM(result_bytes) AS INTEGER) AS result_bytes, CAST(SUM(encode_seconds) AS INTEGER) AS encode_seconds,
       CAST(SUM(failed) AS INTEGER) AS failed
FROM transcode_stat GROUP BY category_id, kind ORDER BY category_id, kind;
