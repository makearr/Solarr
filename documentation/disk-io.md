# Disk IO

Solarr shares each disk between the work that wants it, and leaves an idle disk alone.

**Layout.** `internal/platform/disktopo` reads `/proc/self/mountinfo` and `/sys` and places each root folder (and the config folder) in a *disk group*: a ZFS pool (`zfs:tank`), a network server (`net:nas`), a physical disk (`disk:sda`, partitions and device-mapper volumes resolved to it) or, failing all that, its filesystem (`fs:<device>`). Filesystems nothing else could place are joined when they report the same size and free space. Settings -> Disk (expert, administrator) shows the result and why, and a group name typed there replaces the detected one (setting `disk.group_overrides`).

**Gate.** `internal/platform/iogate` gives each group slots (2; 4 for the shared `*` group; fewer while the disk answers slowly). The classes, most urgent first: playback, import, rename, tag write-back, drain, scan, probe, statistics, recycle bin, previews. The last six classes can never take the final slot, so urgent work always finds room; a waiter moves up one class per 30 seconds it waits, so nothing starves. The job executor (`jobs.DiskGate`) takes a slot after the lease is renewing and releases it when the handler returns. Only scan and rescan jobs name their root, so they get their own disk's group; every other job type goes through `*` until its payload names a disk.

**Latency.** `internal/platform/disklatency` times a directory read per group. It runs only while a job waits for a group (rate limited to once per 5 s per group) or when an administrator presses Measure now. The scheduled capacity and reachability checks skip a root whose group was unused for 30 minutes and which already has a reading.

**Child processes.** ffprobe, fpcalc and the other background tools run under `ionice -c2 -n7`; the playback conversion under `-c2 -n0`. Without the `ionice` tool they run unchanged.

Not built yet: playback does not take a gate slot (its conversion has the ionice priority only), and the change-watching half (inotify budget, last-scan records, scan cadence) is stage 3.
