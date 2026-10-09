-- Candidates and decisions. Column lists follow table order.

-- name: UpsertCandidate :one
INSERT INTO candidate (indexer_id, guid, title, attributes_id, size_bytes, seeders,
                       leechers, published_at, protocol, download_url,
                       parse_confidence, source, first_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (indexer_id, guid) DO UPDATE SET
  title = excluded.title,
  seeders = excluded.seeders,
  leechers = excluded.leechers,
  expires_at = excluded.expires_at
RETURNING id, indexer_id, guid, title, attributes_id, size_bytes, seeders, leechers,
          published_at, protocol, download_url, parse_confidence, source,
          first_seen_at, expires_at;

-- name: GetCandidate :one
SELECT id, indexer_id, guid, title, attributes_id, size_bytes, seeders, leechers,
       published_at, protocol, download_url, parse_confidence, source,
       first_seen_at, expires_at
FROM candidate WHERE id = ?;

-- name: RecordDecision :one
INSERT INTO candidate_decision (candidate_id, desired_item_id, category_id, outcome, score,
                                components_json, rejections_json, deferrals_json,
                                match_confidence, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, candidate_id, desired_item_id, category_id, outcome, score, components_json,
          rejections_json, deferrals_json, match_confidence, decided_at;

-- The interactive search view's own listing (F16): a decision means nothing without the release it was decided about.
-- name: ListDecisionsForItemWithCandidate :many
SELECT cd.id, cd.candidate_id, cd.desired_item_id, cd.outcome, cd.score, cd.components_json,
       cd.rejections_json, cd.deferrals_json, cd.match_confidence, cd.decided_at,
       c.indexer_id, c.guid, c.title, c.size_bytes, c.seeders, c.leechers, c.published_at,
       c.protocol, c.download_url, c.parse_confidence, c.source, c.first_seen_at, c.expires_at
FROM candidate_decision cd
JOIN candidate c ON c.id = cd.candidate_id
WHERE cd.desired_item_id = ? ORDER BY cd.decided_at DESC, cd.id ASC LIMIT ?;

-- Ids rise with time, so the oldest page holds the oldest decisions; a page with none past the line ends the pass.
-- name: ListDecisionsToRetire :many
SELECT cd.id, cd.desired_item_id, cd.outcome, cd.score, cd.rejections_json, cd.decided_at
FROM candidate_decision AS cd
WHERE cd.id IN (SELECT oldest.id FROM candidate_decision AS oldest ORDER BY oldest.id LIMIT sqlc.arg(page_size))
  AND cd.decided_at < sqlc.arg(decided_before)
ORDER BY cd.id;

-- name: DeleteDecisionsByID :execrows
DELETE FROM candidate_decision WHERE id IN (SELECT value FROM json_each(sqlc.arg(ids)));

-- A candidate goes once it expired and no decision refers to it; a held release carries its own copy.
-- name: DeleteUnreferencedExpiredCandidates :execrows
DELETE FROM candidate WHERE id IN (
  SELECT expired.id FROM candidate AS expired
  WHERE expired.expires_at <= sqlc.arg(now)
    AND NOT EXISTS (SELECT 1 FROM candidate_decision d WHERE d.candidate_id = expired.id)
  ORDER BY expired.expires_at
  LIMIT sqlc.arg(page_size)
);

-- name: RollUpDecisions :exec
INSERT INTO decision_rollup (desired_item_id, period, outcome, count, best_score, top_reason)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT (desired_item_id, period, outcome) DO UPDATE SET
  count = decision_rollup.count + excluded.count,
  best_score = MAX(COALESCE(decision_rollup.best_score, excluded.best_score), excluded.best_score),
  top_reason = excluded.top_reason;

-- name: ListDecisionRollup :many
SELECT desired_item_id, period, outcome, count, best_score, top_reason
FROM decision_rollup
WHERE desired_item_id = ?
ORDER BY period DESC, outcome;

-- Release decisions since a time, per library and outcome.
-- name: AggregateDecisionsByCategoryAndOutcome :many
SELECT category_id, outcome, CAST(COUNT(*) AS INTEGER) AS decisions
FROM candidate_decision
WHERE decided_at >= sqlc.arg(since)
GROUP BY category_id, outcome
ORDER BY category_id, outcome;

-- The copy from library.db keeps every id; a row already copied is left as it is.
-- name: CopyCandidate :exec
INSERT OR IGNORE INTO candidate (id, indexer_id, guid, title, attributes_id, size_bytes, seeders, leechers,
                                 published_at, protocol, download_url, parse_confidence, source, first_seen_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CopyDecision :exec
INSERT OR IGNORE INTO candidate_decision (id, candidate_id, desired_item_id, category_id, outcome, score, components_json,
                                          rejections_json, deferrals_json, match_confidence, decided_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: CopyDecisionRollup :exec
INSERT OR IGNORE INTO decision_rollup (desired_item_id, period, outcome, count, best_score, top_reason)
VALUES (?, ?, ?, ?, ?, ?);

-- Where a copy resumes.
-- name: LastCandidateID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) AS id FROM candidate;

-- name: LastDecisionID :one
SELECT CAST(COALESCE(MAX(id), 0) AS INTEGER) AS id FROM candidate_decision;

-- name: CountDecisionRollup :one
SELECT CAST(COUNT(*) AS INTEGER) AS total FROM decision_rollup;
