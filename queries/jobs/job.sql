-- name: EnqueueJob :one
INSERT INTO job (
  type, pool, action_key, priority, payload_json, state, mutating,
  max_attempts, run_after, depends_on, correlation_id, user_id,
  created_at, updated_at, category_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id, type, pool, action_key, priority, payload_json, state, mutating,
          attempts, max_attempts, run_after, lease_owner, lease_expires_at,
          depends_on, correlation_id, user_id, last_error, created_at, updated_at, category_id;

-- name: GetJob :one
SELECT id, type, pool, action_key, priority, payload_json, state, mutating,
       attempts, max_attempts, run_after, lease_owner, lease_expires_at,
       depends_on, correlation_id, user_id, last_error, created_at, updated_at, category_id
FROM job WHERE id = ?;

-- name: FindActiveJobByActionKey :one
SELECT id, type, pool, action_key, priority, payload_json, state, mutating,
       attempts, max_attempts, run_after, lease_owner, lease_expires_at,
       depends_on, correlation_id, user_id, last_error, created_at, updated_at, category_id
FROM job
WHERE action_key = ?
  AND state IN ('PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING');

-- One conditional UPDATE ... RETURNING, so two dispatchers cannot hand the same
-- job to two workers. depends_on keeps a job unclaimable until its parent succeeds.

-- name: ClaimJobs :many
UPDATE job
SET state = 'LEASED',
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = sqlc.arg(lease_expires_at),
    updated_at = sqlc.arg(now)
WHERE id IN (
  SELECT candidate.id FROM job AS candidate
  WHERE candidate.pool = sqlc.arg(target_pool)
    AND candidate.state IN ('PENDING', 'PENDING_RETRY')
    AND candidate.run_after <= sqlc.arg(now)
    AND (candidate.depends_on IS NULL
         OR EXISTS (SELECT 1 FROM job AS parent
                    WHERE parent.id = candidate.depends_on AND parent.state = 'SUCCEEDED'))
  ORDER BY candidate.priority DESC, candidate.run_after ASC
  LIMIT sqlc.arg(capacity)
)
RETURNING id, type, pool, action_key, priority, payload_json, state, mutating,
          attempts, max_attempts, run_after, lease_owner, lease_expires_at,
          depends_on, correlation_id, user_id, last_error, created_at, updated_at, category_id;

-- name: StartJob :exec
UPDATE job
SET state = 'RUNNING', attempts = attempts + 1, updated_at = ?
WHERE id = ? AND lease_owner = ? AND state = 'LEASED';

-- Renewal is conditional on still owning the lease. A worker whose lease was
-- reaped must not be able to take it back and race the worker that has it now.

-- name: RenewLease :execrows
UPDATE job
SET lease_expires_at = ?, updated_at = ?
WHERE id = ? AND lease_owner = ? AND state IN ('LEASED', 'RUNNING');

-- name: CompleteJob :execrows
UPDATE job
SET state = 'SUCCEEDED', lease_owner = NULL, lease_expires_at = NULL,
    last_error = NULL, updated_at = ?
WHERE id = ? AND lease_owner = ?;

-- name: FailJob :execrows
UPDATE job
SET state = 'FAILED', lease_owner = NULL, lease_expires_at = NULL,
    last_error = ?, updated_at = ?
WHERE id = ? AND lease_owner = ?;

-- name: RetryJob :execrows
UPDATE job
SET state = 'PENDING_RETRY', lease_owner = NULL, lease_expires_at = NULL,
    run_after = ?, last_error = ?, updated_at = ?
WHERE id = ? AND lease_owner = ?;

-- name: CancelJob :execrows
UPDATE job
SET state = 'CANCELLED', lease_owner = NULL, lease_expires_at = NULL,
    last_error = sqlc.arg(reason), updated_at = sqlc.arg(now)
WHERE id = sqlc.arg(id)
  AND state IN ('PENDING', 'PENDING_RETRY', 'LEASED', 'RUNNING');

-- The reaper returns abandoned work to the queue. A crashed process therefore
-- self-heals with no manual cleanup, which is what makes restart recovery real.

-- name: ReclaimExpiredLeases :many
UPDATE job
SET state = 'PENDING_RETRY', lease_owner = NULL, lease_expires_at = NULL,
    last_error = sqlc.arg(reason), updated_at = sqlc.arg(now)
WHERE state IN ('LEASED', 'RUNNING') AND lease_expires_at <= sqlc.arg(now)
RETURNING id, type, pool, attempts, max_attempts;

-- Cursor pagination, never OFFSET (Invariant 32). The id tie-break is not optional:
-- updated_at is not unique. A zero cursor is the start, hence the guarded comparison.

-- name: ListJobsPage :many
SELECT id, type, pool, action_key, priority, payload_json, state, mutating,
       attempts, max_attempts, run_after, lease_owner, lease_expires_at,
       depends_on, correlation_id, user_id, last_error, created_at, updated_at, category_id
FROM job
WHERE (sqlc.arg(state) = '' OR state = sqlc.arg(state))
  -- In the query, not afterwards: filtering results returns short pages and
  -- breaks the cursor (Invariant 36).
  AND ((category_id IS NULL AND sqlc.arg(include_system) = 1)
       OR category_id IN (SELECT value FROM json_each(sqlc.arg(visible_categories))))
  AND (sqlc.arg(after_sort) = 0
       OR updated_at < sqlc.arg(after_sort)
       OR (updated_at = sqlc.arg(after_sort) AND id < sqlc.arg(after_id)))
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(page_size);

-- name: CountJobsByState :many
SELECT state, pool, count(*) AS total FROM job GROUP BY state, pool;

-- name: RecordAttemptStart :one
INSERT INTO job_attempt (job_id, attempt, lease_owner, started_at)
VALUES (?, ?, ?, ?)
RETURNING id, job_id, attempt, lease_owner, started_at, finished_at, outcome, error;

-- name: RecordAttemptFinish :exec
UPDATE job_attempt SET finished_at = ?, outcome = ?, error = ? WHERE id = ?;

-- name: ListAttempts :many
SELECT id, job_id, attempt, lease_owner, started_at, finished_at, outcome, error
FROM job_attempt WHERE job_id = ? ORDER BY attempt;

-- name: DeleteFinishedJobsBefore :execrows
DELETE FROM job
WHERE state IN ('SUCCEEDED', 'FAILED', 'CANCELLED') AND updated_at < ?;
