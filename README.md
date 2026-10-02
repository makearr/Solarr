<p align="center"><img src="web/public/icon-192.png" alt="Solarr" width="96"></p>

# Solarr

**Your music library, managed properly.** Solarr finds out what you have, what's missing and what
could be better, then goes and gets it. It is a new engine built to replace the \*Arr apps, starting
with Lidarr.

> **Pre-release, music only, usenet only.** The Lidarr-replacement milestone is largely built and is
> being verified against a real library. TV and movies are designed but not built. Expect rough
> edges, and don't point it at the only copy of anything you care about. See
> [What works today](#what-works-today).

---

## Why switch from Lidarr?

Lidarr is good at the tidy case: sorted folders, well-tagged files, one album at a time. Solarr is
built for the messy case, and for the people who run a library across several disks for years.

### 1. Import anything, from anywhere

Point Solarr at **any folder, at any depth**: one album, a whole collection, a messy download
directory. It works out what's inside from tags, tracklists, durations and folder names. No
pre-sorting, no `Artist/Album` layout required.

You see a **preview first**, then commit, and you can **undo the whole import in one click** if you
don't like what it did.

### 2. Packs, box sets and archives just work

A discography or a 3-CD box set is **one download that becomes many correctly named albums**.
Archives (zip, rar, 7z, tar) are read in place and **never unpacked into your library**.

### 3. Nothing is left in limbo

Music that nothing recognises still becomes a visible, unverified album instead of sitting in an
import screen forever. Fix it later, in the library, with "Identify as…".

### 4. Libraries that span many disks

A library can have any number of root folders. An artist stays on one disk, new content goes where
there's room, and **retiring a disk is one button**: Solarr previews the move, checks the space and
drains it. Folders can be read-only or declared offline without anything being treated as missing
and re-downloaded.

### 5. It tells you why

Every decision keeps its reasons: why a release was rejected, why an album isn't being searched yet,
why downloads are paused. Open an album and read it. No digging through logs.

### 6. Quality that means it

The **file is the evidence, not the release name.** Solarr measures what it downloaded, so fake
upgrades are refused, ties keep what you have, and a bigger number doesn't automatically win (192 kHz
does not beat a clean 48 kHz just for being larger).

### 7. Fakes and junk, handled in one click

**Remove and block** recycles the file, blocks its exact bytes (renaming it can't sneak it back),
blocklists the release and puts the album back on the want list.

### 8. Safe by default

Nothing is deleted directly, it goes to a recycle bin first. Every move is copy → verify → repoint →
delete. A download is never removed before the library copy is verified. Solarr never takes
ownership of your media.

### 9. Easy to run, easy to use

One small container, no database server or message broker. Everything is done from the browser,
and settings come in **normal / advanced / expert** levels so you only see what you need. Multiple
users can have access to different libraries.

### 10. Move over without starting again

Read your existing **Lidarr** and **Prowlarr** setup (it is only ever read, never changed). Naming
defaults match Lidarr's, so adopting an existing library proposes **no renames**.

---

## What works today

| | |
|---|---|
| **Library** | Libraries with their own folders, quality and naming · artists, albums and editions (remasters, deluxe) · posters, table and list views · add artist or album with monitoring · calendar (basic) |
| **Recognition** | Embedded MusicBrainz IDs → whole-tracklist matching → per-file search → folder-name evidence · optional AcoustID fingerprinting · BrainzMash, Lidarr's metadata server and MusicBrainz, tried in order |
| **Finding music** | Newznab indexers (Prowlarr works as-is), searched in parallel · automatic and RSS search · manual search with scores and rejection reasons |
| **Downloading** | SABnzbd · queue, history, held releases, free-space protection · failed releases are blocklisted and re-searched |
| **After download** | Import, rename by template (with a preview), tag write-back of MusicBrainz IDs, reflink → hardlink → copy |
| **Upgrades** | Better files replace worse ones in place, never the other way round |
| **Running it** | Web UI · multiple users · encrypted secrets · automatic backups · scheduled tasks you can run on demand |

## Performance

Numbers measured on the developer's machine, not guarantees. The shape of the thing: the database
is fast, and a first scan is limited by how quickly your disks can be read.

**A real library, first scan.** About 200,000 audio files (16,000 folders, 700+ artists) on an NFS
NAS, scanned and identified from an empty database:

| | |
|---|---|
| Files identified | **99.9%** (202,734 of 202,860); the other 126 are in a review queue |
| Time | roughly **1 h 10 min to 3 h 35 min** across recent runs, depending on NAS load |
| Bottleneck | the NAS: about 25 random reads per second, and a cold file costs about 7 reads. Solarr keeps it busy rather than idle |

An interrupted scan resumes from where it stopped.

**The database, on a synthetic 500,000-file library:**

| Operation | Typical (p50) | Worst case (p99) | Target |
|---|---|---|---|
| A page of the library list, filtered | 1.7 ms | 3.5 ms | 100 ms |
| Search-as-you-type over titles | n/a | 17 ms | 150 ms |
| Import 200 rows | 3.7 ms | 5.7 ms | 50 ms |
| A user's edit while a bulk write runs | 4.3 ms | 19 ms | 25 ms |

These targets are enforced by the test suite (`make bench`, see
[benchmarks](documentation/benchmarks.md)). Lists use cursors, so page 1,000 costs the same as page 1.

**Everything else:** one static binary, SQLite only, no database server or broker. Metadata lookups
from BrainzMash answer in about 0.4 to 1.1 s and are cached; each source has its own rate limit.

## Not there yet

Being honest about the gaps:

- **Usenet only.** Music is often on torrents, so expect thinner results until qBittorrent support
  lands (it's next).
- **Music only.** TV and movies are planned in the same app, after music is solid.
- **Artist aliases:** an artist renamed upstream can show up twice. A fix and a merge tool are next.
- **Archives containing music** are being made to behave exactly like folders, for imports and for downloads.
- **Automatic upgrade hunting** and the *Cutoff Unmet* search button are not open yet.
- **Notifications, statistics, richer calendar views** and an Edit dialog on every item are coming.
- **Lidarr import** is built but hasn't been re-verified against a live Lidarr since its options
  were expanded. It is read-only either way.
- **Deduplicating the same file in two places** (the same song under two artists, or in two libraries) is
  planned, not built. Solarr will recognise it (by content, even across formats) and keep one copy
  linked from both places with a reflink or hardlink. Links only work within one filesystem, so on
  separate disks you'll choose between keeping a duplicate and a symlink.
- Not API-compatible with the \*Arrs, on purpose: their data model can't express editions, packs or
  files shared between libraries.

## Roadmap

1. **Finish the Lidarr replacement:** aliases, upgrade hunting, notifications, statistics, calendar, views,
   and **archives that contain music imported correctly** (zip, rar, 7z, tar, multi-part, read in place).
2. **Torrents** (qBittorrent) and delay profiles.
3. **Coexistence:** run beside your existing \*Arrs and compare, then import from Sonarr and Radarr.
4. **TV**, then **movies**.
5. **Linking and deduplication:** find the same file in two libraries or folders and share one copy via reflink or hardlink.
6. **Discovery** from your own library first, then subscriptions and external lists.
7. Later: books, audiobooks, comics and other media types.

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
2. **Connect an indexer and SABnzbd** under *Connections*, or pull them from Prowlarr with
   *Connections → Arr*. The dashboard tells you what's still missing.
3. **Add music** with *Add new* or *Discover*, choosing whether to monitor it and search straight away.
4. **Let it run.** Solarr checks RSS and searches for missing albums on a schedule.
5. **Follow along** in *Activity → Queue / History* and on each album's page, which says when it was
   last searched, when it will be again, or why it won't be.

Settings are edited in the UI and can be exported as YAML. `config.yml` only holds bootstrap values.
An invalid one stops startup with every error listed at once.

---

## Solarr and Lidarr, side by side

| | Lidarr | Solarr |
|---|---|---|
| Media types | Music | Music now; TV and movies planned in the same app |
| Import | Sorted folders, one at a time | Any folder, any depth, previewed, fully revertible |
| Packs and box sets | Weak | One download → many albums |
| Archives | Extracted | Read in place |
| Unrecognised music | Stuck in the import screen | Visible as an unverified album |
| Fakes | Blocklist by release name | Blocks the exact bytes, re-wants the album |
| Quality | Ordered format list | Gates, tiers and a score, judged from the real file; ties keep yours |
| Why did it do that? | A rejection string | Structured reasons kept per decision |
| Several disks | Root folders | Affinity, read-only / offline / draining, one-button drain |
| Deleting | Optional recycle bin | Always recycled first |
| Users | One login | Multiple, with per-library access |
| Settings | One long list | Normal / advanced / expert |
| Deployment | .NET app | One Go binary, SQLite only |
| **Torrents** | Yes | **Not yet** |
| **Notifications** | Many connectors | **Not yet** |
| **Maturity** | Years in production | Pre-release |

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
