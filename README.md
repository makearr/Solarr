<p align="center"><img src="web/public/icon-192.png" alt="Solarr" width="96"></p>

# Solarr

**Your media library, managed properly.** Solarr finds out what you have, what's missing and what
could be better, then goes and gets it. It is a new engine built to replace the \*Arr apps in one
program: music (the Lidarr replacement) and TV (the Sonarr replacement) work today, movies are next.

> **Under heavy development.** Not every expected feature is available yet, and the layout of the
> interface and the **database schema are still subject to change** between versions. Expect rough
> edges, and don't point it at the only copy of anything you care about. See
> [What works today](#what-works-today) and [Not there yet](#not-there-yet).

---

## What makes it different

Lidarr and Sonarr are good at the tidy case: sorted folders, well-tagged files, one library each.
Solarr is built for the messy case, and for people who run big libraries across several disks for years.

### One app, many libraries

A **library** is a named collection with its own root folders, quality profile, naming template,
language preferences and users. One Solarr runs as many as you like: a music library, a second one for
live recordings, TV, anime. That is what you now do with several Lidarr and Sonarr instances. Each
library has its own navigation (its own lists, Wanted, Insights, Files, Settings), and people (actors, artists,
directors) are shared between them: an actor who is also a musician is one person with both
careers on one page.

### Hunting: it goes and finds things

Lidarr waits for you to press *Search all*. Solarr **hunts**. A planner works out which wanted items
may be searched and how urgently (new releases first, then the rest, with a backoff that doubles
from 6 hours to a week), and a scheduled hunt searches the most urgent ones within an hourly
budget you set. Alongside it:

- **RSS sync** grabs new releases the moment an indexer lists them.
- **Upgrade hunting** looks for better copies of what you already own, a fifth of the budget, and only
  replaces a file when the new one *strictly* wins.
- **Free-space gating**: nothing is sent that would not fit, counting what is already downloading.
- **Failure memory**: failed or fake releases are blocklisted by release *and* by content hash, and
  the item goes back on the want list.
- It **explains itself**: an album's page says when it was last searched, when it will be again, or
  why it won't be (no indexer, no free space, not released yet).

### Better file recognition

Point Solarr at **any folder, at any depth**: one album, a whole collection, a messy download
directory. It does not need `Artist/Album` layout. It identifies by evidence, in order: embedded
MusicBrainz IDs, **whole-tracklist matching** (durations, positions, titles), per-file search, folder
names, and optionally **AcoustID audio fingerprinting** for files with no tags at all. On a real
library of about 200,000 files, **99.9% were identified** on the first scan.

You get a **preview first**, then commit, and you can **undo the whole import in one click**.
Packs and box sets (a discography is one download that becomes many correctly named albums) and
archives (zip, rar, 7z, tar, read in place, **never unpacked into your library**) just work. Music
nothing recognises becomes a visible, *unverified* album instead of sitting in an import screen for
ever: fix it later with "Identify as...". For TV, Sonarr's own release-name parser handles episodes,
seasons, daily shows, anime absolute numbering and multi-episode files.

### Libraries that span many disks

A library can have any number of root folders. An artist or series stays on one disk, new content goes
where there's room, and **retiring a disk is one button**: Solarr previews the move, checks the space
and drains it. A folder can be read-only, or declared offline for good, without anything on it being
treated as missing and re-downloaded. Every move is one engine: copy, verify, repoint, then delete.

### Deduplication

Two libraries (or two artists) that want the same file share **one copy**. Solarr finds identical
files and links them with a **reflink, else a hardlink**, after showing you a preview. A separate
report lists likely duplicates by metadata (same track on different albums) and recommends the copy
closer to your target quality. Links only work within one filesystem; across disks the plan is that
you choose between keeping a duplicate and a symlink (not built yet).

### Quality that means it

The **file is the evidence, not the release name.** Solarr measures what it downloaded with ffprobe,
so fake upgrades are refused, ties keep what you have, and a bigger number doesn't automatically win
(192 kHz does not beat a clean 48 kHz just for being larger). Profiles cover formats, codecs, bit
depth, resolution, HDR, subtitle and audio languages, and preferred release groups. **Remove and
block** recycles a bad file, blocks its exact bytes (renaming it can't sneak it back) and re-wants it.

### Safe by default

Nothing is deleted directly: it goes to a recycle bin first. A download is never removed before the
library copy is verified, a download client's queue is never touched beyond Solarr's own category, and
Solarr never takes ownership of your media.

### Easy to run, easy to use

One small container, no database server, no message broker. Everything is done from the browser, and
settings come in **normal / advanced / expert** levels so you only see what you need. Multiple users
can have different access to different libraries. Every decision keeps its reasons, so "why did it
do that?" is answered on the page, not in a log file.

### Move over without starting again

Read your existing **Lidarr**, **Sonarr** and **Prowlarr** setup (it is only ever *read*, never
changed). Naming defaults match Lidarr's, so adopting an existing library proposes **no renames**.

---

## What works today

| | |
|---|---|
| **Libraries** | Music and TV libraries, each with their own folders, quality, naming and users · posters, overview and table views · custom, saveable filters · bulk edit · tags · calendar (basic) |
| **Music** | Artists, albums and editions (remasters, deluxe) · artist aliases · add artist or album with monitoring · Navidrome-friendly tags and covers (optional) |
| **TV** | Series, seasons, episodes, anime and daily shows · Sonarr's add options and monitor modes · People, Networks and Follow · Wanted and Cutoff unmet · specials and absolute numbering |
| **Recognition** | Embedded IDs → tracklist matching → per-file search → folder names → optional AcoustID · music from BrainzMash, Lidarr's metadata server and MusicBrainz, tried in order · TV from Skyhook, TVmaze, TMDB, TheTVDB and AniList (TMDB wants your own free key) |
| **Finding** | Newznab/Torznab indexers (Prowlarr works as-is), searched in parallel · hunting, RSS, upgrade hunting · manual search with scores and rejection reasons · delay profile for preferring usenet or torrents |
| **Downloading** | **SABnzbd** and **qBittorrent** · queue, history, held releases, free-space protection · optional seeding limit per client (ratio or time) · failed releases blocklisted and re-searched |
| **After download** | Import, rename by template (with preview), tag write-back of MusicBrainz IDs, reflink → hardlink → copy · upgrades replace files in place, never the other way round |
| **Running it** | Web UI · multiple users · notifications centre · statistics · encrypted secrets · automatic backups · scheduled tasks you can run on demand · YAML settings export |

## Architecture, and why it is fast

Solarr is **one static Go binary** with the web interface embedded. There is no .NET runtime, no
external database and no message broker.

**Four small SQLite databases instead of one large one**, each with a single writer:

| Database | Holds | Why separate |
|---|---|---|
| `settings.db` | Configuration and credentials (secrets encrypted) | Tiny, and **fully cached in memory**: reading a setting never touches disk |
| `library.db` | Everything that scales with your media: works, files, people, desired state | The big one. Reads never wait for writes |
| `jobs.db` | The work queue and event log | High churn, kept away so a burst of background jobs never delays a library write |
| `statistics.db` | What was seen and done: download history, every release a search found and why it was taken or refused, figures over time | Append-mostly. The library stays correct with it empty, so it can grow or be pruned freely |

A cache directory holds artwork and metadata responses and can be deleted at any time. Databases are
backed up automatically and before every migration, and they must live on local storage.

Why this is quick compared with the \*Arrs:

- **Reads and writes don't block each other.** SQLite runs in WAL mode with a pool of readers and
  one writer behind a priority queue, so an edit you make in the UI jumps ahead of a bulk scan.
  Transactions stay under about 50 ms and bulk work is done in 200-row chunks; a CI test fails if one doesn't.
- **Built for 500,000+ files.** Partial indexes, cursor pagination (page 1,000 costs the same as page 1),
  and counters kept up to date as things change, so a dashboard number is a lookup, never a `COUNT(*)`.
- **Work is planned, not scanned.** A desired-state planner reads the database and emits actions; it
  does no I/O. Only what changed is re-examined, so it never re-walks your whole library to find out
  what's missing.
- **Parallel where it helps.** Indexers are searched at once with a deadline, metadata providers each get
  their own rate-limit bucket, and scans, probes and moves run in separate worker pools.
- **Measured, not claimed.** Speed targets are enforced as tests (`make bench`).
  See [database layout](documentation/database-layout.md) and [benchmarks](documentation/benchmarks.md).

## Performance

Numbers measured on the developer's machine, not guarantees.

**A real library, first scan.** About 200,000 audio files (16,000 folders, 700+ artists) on an NFS
NAS, scanned and identified from an empty database: **99.9% identified** (the rest go to a review
queue), in roughly **1 h 10 min to 3 h 35 min** depending on NAS load. The bottleneck is the NAS, not
Solarr. An interrupted scan resumes where it stopped.

**The database, on a synthetic 500,000-file library:**

| Operation | Typical (p50) | Worst case (p99) | Target |
|---|---|---|---|
| A page of the library list, filtered | 1.7 ms | 3.5 ms | 100 ms |
| Search-as-you-type over titles | n/a | 17 ms | 150 ms |
| Import 200 rows | 3.7 ms | 5.7 ms | 50 ms |
| A user's edit while a bulk write runs | 4.3 ms | 19 ms | 25 ms |

A TV library of about 1,000 series and 85,000 episodes (450,000 credits) answers every list in
under 0.25 s.

## Not there yet

- **Movies** (the Radarr replacement) are designed, not built. Books, audiobooks and comics come later.
- **Deduplication** is previewed and working at the file level but has had little real-world use;
  matching the same audio across formats (Chromaprint) and the duplicate-or-symlink choice across
  disks are not built.
- **Torrents** work end to end but have been exercised less than usenet; magnet-only indexer links are
  not tried.
- **RSS grabs** are tested but have not yet been seen catching a brand-new release live.
- **Notifications** are an in-app centre only; webhooks and per-service connectors come later.
- **Discovery** from your own library, subscriptions and external lists is partial.
- **Radarr import** doesn't exist yet; Lidarr and Sonarr import do. Lidarr import is slow on very
  large libraries (hours).
- Not API-compatible with the \*Arrs, on purpose: their data model can't express editions, packs or
  files shared between libraries.
- The about-page source link and the container image are still private.

## Roadmap

1. **Finish and release the Lidarr and Sonarr replacements:** the remaining acceptance checks against live setups.
2. **Movies**, and import from Radarr.
3. **Coexistence:** run beside your existing \*Arrs and compare.
4. **Deduplication** across formats and disks.
5. **Discovery** from your own library first, then subscriptions and external lists.
6. Later: books, audiobooks, comics and other media types.

---

## Installation

Solarr ships as one Docker image with everything it needs (`ffprobe`, `fpcalc`, the tag writer).

> The image is at `ghcr.io/makearr/solarr`, currently **private**. If you can't pull it, build it
> yourself (below).

```yaml
# docker-compose.yml
services:
  solarr:
    image: ghcr.io/makearr/solarr:latest
    container_name: solarr
    restart: unless-stopped
    environment:
      PUID: 1000
      PGID: 1000
      TZ: Europe/Berlin
    ports:
      - "5454:5454"
    volumes:
      - ./config:/config        # settings, databases, backups: local disk, not NFS/SMB
      - /media:/media           # your library
      - /downloads:/downloads   # the same path your download client reports
```

Then open **http://localhost:5454**. There's also a ready-made [docker-compose.yml](docker-compose.yml).

Things worth knowing:

- **`PUID`/`PGID`** set who Solarr runs as. It takes ownership of its own folders only, never your
  media. Don't set `user:`.
- **Keep `/config` on local storage.** SQLite doesn't work reliably over NFS/SMB.
- **Hardlinks need one filesystem.** Put the library and the completed-downloads folder on the same
  mount, or Solarr copies instead.
- Port 5454 can be changed with `SOLARR_SERVER_PORT` or on the Network screen.

### Build from source

Toolchains run in Docker; no local Go or Node is needed.

```sh
make verify        # format, vet, lint, tests
make web           # build the frontend
make image-local   # build a local image
```

---

## First steps

The full walk-through is the [user guide](documentation/user-guide.md). In short:

1. **Setup wizard:** create the administrator and your first library (a name and a folder). Solarr
   scans it; covers and artist photos arrive over the next few minutes.
2. **Connect an indexer and a download client (SABnzbd or qBittorrent)** under *Connections*, or pull them from Prowlarr with
   *Connections → Arr*. The dashboard tells you what's still missing.
3. **Add music** with *Add new* or *Discover*, choosing whether to monitor it and search straight away.
4. **Let it run.** Solarr checks RSS and searches for missing albums on a schedule.
5. **Follow along** in *Activity → Queue / History* and on each album's page, which says when it was
   last searched, when it will be again, or why it won't be.

Settings are edited in the UI and can be exported as YAML. `config.yml` only holds bootstrap values.
An invalid one stops startup with every error listed at once.

---

## Solarr and the \*Arrs, side by side

| | Lidarr / Sonarr | Solarr |
|---|---|---|
| Media types | One per app | Music and TV in one app, movies planned |
| Libraries | One per instance | Any number per app, each with its own folders, quality and users |
| Searching | You press Search all | A planner hunts within a budget, plus RSS and upgrade hunting |
| Import | Sorted folders, one at a time | Any folder, any depth, previewed, fully revertible |
| Packs and box sets | Weak | One download → many albums |
| Archives | Extracted | Read in place |
| Unrecognised music | Stuck in the import screen | Visible as an unverified album |
| Fakes | Blocklist by release name | Blocks the exact bytes, re-wants the item |
| Quality | Ordered format list | Gates, tiers and a score, judged from the real file; ties keep yours |
| Why did it do that? | A rejection string | Structured reasons kept per decision |
| Several disks | Root folders | Affinity, read-only / offline / draining, one-button drain |
| Same file twice | Two copies | Linked copies (reflink or hardlink) after a preview |
| Deleting | Optional recycle bin | Always recycled first |
| Users | One login | Multiple, with per-library access |
| Settings | One long list | Normal / advanced / expert |
| Deployment | .NET app, one database | One Go binary, three small SQLite databases |
| Notifications | Many connectors | In-app centre only for now |
| **Maturity** | Years in production | Under heavy development |

---

## More

- **Documentation:** [documentation/](documentation/README.md): [user guide](documentation/user-guide.md),
  [music identification](documentation/music-identification.md), [naming](documentation/naming.md),
  [downloads](documentation/downloads.md), [file management](documentation/file-management.md),
  [container runtime](documentation/container-runtime.md).
- **Contributing:** Go, SQLite with sqlc, OpenAPI spec-first, React frontend. Start at
  [codebase-map.md](documentation/codebase-map.md) and [coding-standards.md](documentation/coding-standards.md);
  `make verify` must pass.
- **Third-party reuse:** [ATTRIBUTION.md](ATTRIBUTION.md). **History:** [CHANGELOG.md](CHANGELOG.md).

## Licence

[AGPL-3.0-or-later](LICENSE). Free to use, modify and self-host. If you run a modified version as a
service for others, you must make your changes available to them.
