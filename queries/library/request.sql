-- The request workflow's table (ADR-14, stage 12). Carried from stage 1 so approval
-- is feature work rather than a schema backfill: nothing is monitored or searched
-- until a request is approved and its desired_item created.

-- name: CreateRequest :one
INSERT INTO request (user_id, work_id, external_ref, media_type, category_id,
                     scope_json, state, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, user_id, work_id, external_ref, media_type, category_id, scope_json,
          state, decided_by, decided_at, decline_reason, created_at;

-- name: GetRequest :one
SELECT id, user_id, work_id, external_ref, media_type, category_id, scope_json,
       state, decided_by, decided_at, decline_reason, created_at
FROM request WHERE id = ?;

-- Oldest first: an approval queue works through its backlog in order. Cursor on id,
-- never OFFSET (Invariant 32); created_at is not unique, so id is the tie-break.
-- name: ListPendingRequests :many
SELECT id, user_id, work_id, external_ref, media_type, category_id, scope_json,
       state, decided_by, decided_at, decline_reason, created_at
FROM request
WHERE state = 'PENDING' AND (sqlc.arg(after_id) = 0 OR id > sqlc.arg(after_id))
ORDER BY id
LIMIT sqlc.arg(page_size);

-- Newest first: a user's own request history.
-- name: ListRequestsForUser :many
SELECT id, user_id, work_id, external_ref, media_type, category_id, scope_json,
       state, decided_by, decided_at, decline_reason, created_at
FROM request
WHERE user_id = ? AND (sqlc.arg(before_id) = 0 OR id < sqlc.arg(before_id))
ORDER BY id DESC
LIMIT sqlc.arg(page_size);

-- Guarded on the current state so approving (or declining) twice -- a race between
-- two admins, or a retried request -- reports that nothing changed rather than
-- silently overwriting who decided it and when.
-- name: DecideRequest :execrows
UPDATE request
SET state = ?, decided_by = ?, decided_at = ?, decline_reason = ?
WHERE id = ? AND state = 'PENDING';

-- name: MarkRequestFulfilled :execrows
UPDATE request SET state = 'FULFILLED' WHERE id = ? AND state = 'APPROVED';
