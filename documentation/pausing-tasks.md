# Pausing tasks

Settings > Scheduler has a **Pause** button on every task and **Pause all tasks** above the list. `PATCH /schedules/{key}` takes `paused`; `PUT /schedules/paused` pauses or resumes every task.

A pause:

- skips the task's scheduled fires and refuses Run now (409);
- stops the task's running jobs, which go back to the queue as `PENDING` with `last_error = "paused"` and keep their attempt;
- keeps every queued job of the task from being claimed, until **Resume**; they then run from where they were put back.

A job that checkpoints its own work (the sweeps skip what is already done) continues; one that does not starts its unit again.

## What a pause covers

`TaskRunner` marks the context it enqueues with the task's key (`jobs.WithTaskKey`); `Queue.Enqueue` records the key in `job_task`. A running job hands the key on to the jobs it enqueues and to its continuation after a yield, so pausing `scan` also holds the work the scan started. Work no task started (a person's search, an import from a download) carries no key and runs on, also under **Pause all**.

## Where it lives

`jobs.db`, migration 0008: `task_pause (scope, paused_at)` - `scope` is a task key or `*` - and `job_task (job_id, task_key)`. A pause survives a restart; `jobs.Pauses.Load` reads it at start-up. `ClaimJobs` skips jobs whose task is paused, so no dispatcher state is involved.
