# RAM copies of the cache and the databases

> **Level: 🔴 Expert.** Off by default. Settings -> Cache, or the `memory:` section of `config.yml`.

Two independent options run a working copy from RAM and write it back to disk on an interval:

| Option | In RAM | Written back |
|---|---|---|
| `cache_tmpfs` | `images/` (pictures not yet in the library) and `metadata.db` | the pictures by file, `metadata.db` by snapshot, every `cache_sync_interval` (10 min) |
| `database_tmpfs` | `library.db`, `settings.db`, `jobs.db`, `statistics.db` | each database that changed, by snapshot, every `database_sync_interval` (5 min) |

A container cannot mount a tmpfs itself, so the RAM directory is `memory.tmpfs_dir` (default `/dev/shm/solarr`). `/dev/shm` is
RAM-backed in every container and **its size is the runtime's `shm_size`** (64 MB by default): raise it in the compose file
(`shm_size: 8gb`) or the options are refused for lack of room. Library covers, previews, backups, the import workspace
and the transcode folder are never put in RAM.

## What can go wrong

- **A crash loses what changed since the last copy** (Solarr killed, out-of-memory kill, power cut, the machine
  rebooting). For the databases that is everything Solarr learned in that time. Backups are separate and unaffected.
- A copy of a database holds its writer for as long as the snapshot takes, so a large `library.db` pauses writes for that
  time at every interval; a database that has not been written since the last copy is skipped.
- RAM is counted by the container's cgroup. The options need the files' size plus 10%.

## The RAM directory must be larger than what goes in

At start the size of `tmpfs_dir` is read and logged. An option is turned on, and is kept on at a start, only when that size
is at least **20% larger than what it would hold as it is on disk now** (the cache's pictures and `metadata.db`; the four
databases; both added when both are on). Settings -> Cache shows the size, the room free, what each option holds on disk
and what each needs, so the number to give `shm_size` is on the page.

## Shutdown

On SIGTERM everything that runs from RAM is copied to disk **at once**, before the running jobs drain, and again after
they have stopped. A container runtime gives a stopping container ten seconds unless told otherwise
(`stop_grace_period` in compose, `docker stop -t`): raise it if the databases are large.

## Turning it on

Settings -> Cache shows what each option would take, the memory the **container** (its cgroup, not the host) may still
use, and the room in `tmpfs_dir`. It is refused (409) when the need is more than either, and it needs "I understand the
risk" ticked. It is written to `config.yml` and applies at the next start. At start, if both options no longer fit, the
cache is dropped first and then the databases, and the page says so.

## When memory runs out

While anything runs from RAM, every 30 seconds the container's free memory is read. Below a twentieth of the limit (at
least 256 MB) Solarr copies what runs from RAM to disk, switches the **cache** off in `config.yml` first, switches the
databases off too only if that would not free enough, and stops. A restart policy brings it back from disk; the page
shows why (`memory-copies.notice` beside `config.yml`, removed at the next save). The stores are shared across the whole
process, so "off" takes effect by that restart rather than by reopening databases under running work.
