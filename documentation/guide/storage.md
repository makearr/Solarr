# Storage, folders and the recycle bin

> **Level: 🟡 Advanced**, administrators. 🔴 sections need the *expert* interface level.

## Root folders (🟡)

**Storage → Folders** holds the root folders, grouped under their library (a root folder belongs to at most
one), and the download folders. Each root's card carries:

- the policy (read only, allow rename, **reserve**: free space kept, 8 GB by default; automatic downloads stop
  before they would eat into it);
- capacity;
- **Preview rename**: names the files even on a root it may not change, and says why;
- **Drain…**: shows what would move and asks before moving every file to the library's other roots.

Removing a folder, or taking a root out of its library, asks first. Startup checks:
[startup-checks.md](../startup-checks.md).

## A root Solarr cannot reach (🟡)

Missing, unreadable, or empty although it held files (an unmounted disk): Solarr declares it **offline**,
checks every five minutes, and declares it online again once reached. A root *you* declare offline stays
offline until you declare it online.

## A file you delete (🟡)

The next **full** scan notices: its album or season is missing again and searched for, unless the library
unmonitors deleted files.

If a full scan finds a great many files of a root gone at once (more than 50 and more than one in twenty), it
marks **none** of them and the bell says so: a disk that was not mounted is not a deleted library. Run a full
scan once the disk is back, or press **Accept as gone** on the root's card.

## Recycle bin (🟡, administrators)

**Storage → Recycle bin** lists what the bins hold, newest first, with the day each file is removed for good.
An administrator can **Empty recycle bin** by hand. Besides the size limits, **Keep free on a root folder** (GB and/or a share of the root) empties a root's bin, oldest first, while the root has less free space than the larger of the two (`recycle_bin.min_free_*`). Nothing in Solarr is deleted directly; it goes here first.
Reference: [file-management.md](../file-management.md).

## Similar videos (🔴)

**Similar videos** (the Duplicates page of TV, movie and other video libraries; administrators see every library's
at **Storage → Duplicates**, with a **Library** menu that defaults to All) lists files that look like one cut in several
encodes. Each file shows the chips of its work page, including its percentage of the library's target, a play button
for a preview, a link to its item in its library and how many of the 64 hash bits differ from the kept file. The file
closest to its library's target is ticked to keep; tick more than one to keep several. Every file that is not kept has
**Merge into the kept file** (its locations become a hardlink or reflink to the kept file, the old copy waits 7 days in
the recycle bin) and **Delete** (recycle bin). Merging needs the same library and the same file kind, otherwise only
Delete is offered. A warning shows when the file names give different episodes (SxxExx).

How it decides: a background pass at the end of the probe sweep (**off by default**, switch it on under Storage →
Duplicates) samples nine moments of every video, crops black bars, and keeps a 64-bit perceptual hash of up to five
frames per moment and of their average (a file ffmpeg cannot decode stays unhashed and is tried again next sweep).
Two files are grouped when:

- their lengths are within 2% or 5 seconds,
- their average frames are within the chosen **similarity level** (exact, high, medium, low or lowest),
- at least two thirds of the moments both files have a usable frame for (at least four; black and flat frames do
  not count) show the same picture within the level's frame distance (10 bits at medium).

Delete is offered on every file, kept ones too, and a whole group can be deleted at once. The algorithm is in
[duplicate-videos.md](../duplicate-videos.md).

Groups are listed largest first. The hash is Solarr's own, not StashDB's. **TV Duplicates** are two files on one
episode record, never two episodes that merely share a title or a length.

## Cache and database memory (🔴)

**Settings → Cache** bounds the provider answers and resized artwork Solarr keeps. Clearing it is always safe.

| Setting | Default |
|---|---|
| Size limit | 10 GiB |
| Age | 90 days |
| Images / metadata split | |
| **Keep disk free** | 10%: when the disk under the cache runs short of that, the next cache maintenance shrinks the cache by the shortfall, below its size limit |

The page shows the disk's size and free space and warns when the size limit is larger than the disk.

**Database memory** sets one ceiling for the SQLite page caches of all databases together (default 384 MiB, at
least 128 MiB; the library database takes 80% of it). It is written to `config.yml` and applied at the next start;
**Set defaults** restores it. A bigger ceiling keeps more of a large library in RAM and makes lists and scans faster.
The page also shows every database's file size, how full its cache is and its hit ratio, beside the hit ratios of the
picture and provider-response caches (counted since the start). **Shrink** drops pictures unused for seven days and
provider answers older than that. Layout of the databases: [database-layout.md](../database-layout.md).

**Run from RAM** (expert, off by default): the cache and/or the four databases can run from a RAM directory
(`/dev/shm/solarr`, a tmpfs) and be copied back to disk on an interval. The container needs `shm_size` large enough,
and a crash loses what changed since the last copy. Details, risks and the memory guard:
[memory-copies.md](../memory-copies.md).

## Moving a configuration (🟡)

Export and import of the settings as YAML: [configuration-export.md](../configuration-export.md).

## Rename files (🟡, administrators)

**Storage → Rename** previews, library by library (or all at once with *Preview every library*), the files whose names differ
from what the library's naming would give, then renames them with the move engine (`POST /categories/{id}/rename`).
