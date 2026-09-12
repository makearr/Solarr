# Startup directory checks

`internal/platform/config.CheckDirectories` runs before anything opens a database
(architecture §14.3). The most common containerised failure is a volume the
process cannot write, discovered at the first import rather than at startup.

## How a directory is checked

By **writing to it**, not by reading its permission bits:

1. `Stat` — create it if absent and the requirement allows creation.
2. Write a `.mnx-writable` probe through `fsx.WriteAtomic`, then remove it.

The probe performs create, rename and delete — the same three operations an
import performs. A directory that passes cannot then fail an import for a reason
this check could have caught.

Inspecting the mode bits instead would pass on a read-only mount, a full
filesystem, and a container whose user is not the one the mount expects. All
three are ordinary deployment mistakes.

## What is deliberately not checked

### Where the databases live

**Putting `db_dir` on a network filesystem is a bad idea, and it is the
operator's responsibility.**

SQLite's WAL mode is explicitly unsupported on NFS and SMB, and their locking is
unreliable enough to corrupt a database. MediaNexus says so — here, in ADR-2,
and in the comments of the example `docker-compose.yml` — and does not detect
it. Media, downloads and backups may live anywhere; only the databases care.

This is a decision, not an omission. Detecting it needs `statfs` and a table of
filesystem magic numbers, carried in the `fsx` abstraction forever, to catch a
mistake that is rare, documented at every point where someone would make it, and
squarely the operator's to make.

### The directory's own ownership

The error from a failed write names the uid and gid the process runs as, and
— since `fsx.FileInfo` carries `UID`/`GID` from `syscall.Stat_t` — also the
directory's own owner and mode, when the filesystem can report them. Invariant
62 is fully met: a PUID/PGID mismatch no longer costs a manual `ls -l`.
