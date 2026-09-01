# Startup directory checks

`internal/config.CheckDirectories` runs before anything opens a database
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

Two things architecture §14.3 asks for are **not implemented**, by decision
rather than oversight. Both need syscalls beyond the `fsx.FS` interface —
`stat` for ownership and `statfs` for filesystem type — and adding them was
weighed against their value for a ZFS deployment and declined.

| Not checked | Consequence | What it would take |
|---|---|---|
| The directory's owner and mode, reported against the process's | **Invariant 62 is only half met.** The error names the uid/gid the process runs as, but not what the directory expects, so a PUID/PGID mismatch still needs one manual `ls -l` | `Uid`/`Gid` on `fsx.FileInfo`, from `syscall.Stat_t` |
| Whether `db_dir` is a network filesystem | **A database on NFS or SMB is accepted.** ADR-2 requires refusing to start, because WAL is unsupported there and the locking is unreliable enough to corrupt a database. This will not bite the current ZFS deployment; it will bite anyone who mounts `/config` over NFS | `statfs` on the path, comparing `f_type` against the network filesystem magic numbers |

Revisit the second one before the image is published for anyone else to run:
silent corruption is the worst failure mode this project has, and it is the one
check that prevents it.
