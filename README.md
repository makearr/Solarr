<p align="center"><img src="web/public/icon-192.png" alt="Solarr" width="96"></p>

<h1 align="center">Solarr</h1>

<p align="center"><b>Your media library, managed properly.</b><br>
One self-hosted app that finds out what you have, what's missing and what could be better, then goes and gets it.<br>
Music, TV, anime and movies in one engine: a successor to Lidarr, Sonarr and Radarr, not a port of them.</p>

<p align="center"><img src="documentation/screenshots/dashboard.png" alt="The dashboard: recently added series and the libraries" width="900"></p>

---

## ⚠️ Early alpha

**Solarr is in early alpha. Use it at your own risk.**

- Features are missing, the interface changes, and the **database schema can still change** between
  versions. Upgrades migrate your data and take a backup first, but don't bet anything on it yet.
- Point it at a **copy** of your library, or mount your library **read-only**, until you trust it.
  Solarr never deletes directly (everything goes to a recycle bin first), but alpha software is alpha
  software.
- Running it beside your existing \*Arrs is the safest way to try it: its \*Arr import only ever *reads*.

**Please tell me about problems**, big or small: wrong matches, confusing screens, slow pages, crashes,
"why did it do that?". Open an [issue](https://github.com/makearr/Solarr/issues) with what you did, what
you expected and what happened (logs help: *Settings → Logs*). Every report makes it better.

---

## At a glance

- **One app, many libraries.** Music, TV, anime and movies side by side, each library with its own
  folders, quality, naming and users. People are shared: an actor who also makes records is one person.
- **It hunts.** A planner decides what to search for and when, inside a budget you set, plus RSS and
  upgrade hunting. You don't press *Search all*.
- **It recognises messy folders.** Any folder, any depth, packs and archives, with a preview before
  anything moves and a one-click undo.
- **The file is the evidence.** Quality is judged from what ffprobe measures, not from the release name.
- **Safe by default.** Everything removed goes to a recycle bin first; your download client's own items
  are never touched.
- **Private by default.** No telemetry, no accounts, no phone-home. It talks to what you configure.
- **One small container.** A Go binary with SQLite inside. No database server, no message broker.

## Screenshots

| | |
|---|---|
| [![Album wall](documentation/screenshots/music-albums.png)](documentation/screenshots/music-albums.png) **Music library**: albums as a poster wall, with table and overview views, filters and bulk edit | [![Album page](documentation/screenshots/album.png)](documentation/screenshots/album.png) **An album**: tracks with the measured format and size of every file |
| [![Artist page](documentation/screenshots/artist.png)](documentation/screenshots/artist.png) **An artist**: biography, aliases, the albums you have and the ones still missing | [![Series wall](documentation/screenshots/tv-series.png)](documentation/screenshots/tv-series.png) **TV library**: series with how much of each is on disk |
| [![Series page](documentation/screenshots/tv-series-page.png)](documentation/screenshots/tv-series-page.png) **A series**: cast and crew, seasons, and per episode the file's resolution, codec and size | [![Series page, light](documentation/screenshots/tv-series-page-light.png)](documentation/screenshots/tv-series-page-light.png) **Light or dark**, following your system |
| [![Import preview](documentation/screenshots/import-preview.png)](documentation/screenshots/import-preview.png) **Import preview**: an untagged two-album pack, both albums recognised from their tracklists | [![Calendar](documentation/screenshots/calendar.png)](documentation/screenshots/calendar.png) **Calendar**: what you have, what's wanted and out, and what isn't out yet |
| [![Statistics](documentation/screenshots/statistics.png)](documentation/screenshots/statistics.png) **Insights**: completeness, the biggest albums, formats and growth | |

<sub>Screenshots are from a demo instance. Its files are silent or blank placeholders, which is why every
track is the same FLAC and every episode the same 1080p H.264.</sub>

---

## What Solarr does that the \*Arr stack doesn't

Lidarr, Sonarr and Radarr are good at the tidy case: sorted folders, well-tagged files, one media type
per app. Solarr is built for the messy case, and for people who run big libraries across several disks
for years.

### One app, many libraries, every media type

A **library** is a named collection with its own root folders, quality profile, naming template,
language preferences, rating limit and users. One Solarr runs as many as you like: music, live
recordings, TV, anime, documentaries, films, kids' films. That is what several Lidarr, Sonarr and Radarr
instances do today. A library can even **mix types** (a "Kids" library with series and films). People
(actors, artists, directors) and tags are shared between libraries, and Solarr **suggests the right
library** when you add something ("an anime: Anime library").

### Hunting: it goes and finds things

A planner works out which wanted items may be searched and how urgently: new releases first, then the
rest, with a backoff that doubles from 6 hours to a week. A scheduled hunt searches the most urgent ones
within an hourly budget you set. Alongside it:

- **RSS sync** grabs new releases the moment an indexer lists them.
- **Upgrade hunting** looks for better copies of what you own with a fifth of the budget, and only
  replaces a file when the new one *strictly* wins.
- **Held releases**: when the download client is full, the best releases wait in priority order and are
  sent as it frees up, so the queue never runs dry.
- **Free-space gating**: nothing is sent that would not fit, counting what is already downloading.
- **Failure memory**: failed or fake releases are blocklisted by release *and* by content hash, and the
  item goes back on the want list.
- **It explains itself**: a work's page says when it was last searched, when it will be again, or why
  it won't be (no indexer, no free space, not released yet, above the library's rating limit).

### Better recognition

Point Solarr at **any folder, at any depth**: one album, a whole collection, a messy download
directory. It identifies by evidence, in order: embedded IDs, **whole-tracklist matching** (durations,
positions, titles), per-file search, folder names and, optionally, **AcoustID audio fingerprints** for
files with no tags at all. On a real library of about 200,000 audio files, **99.9% were identified** on
the first scan.

You get a **preview first**, then commit, and you can **undo the whole import in one click**. Packs and
box sets (one download that becomes many correctly named albums or seasons) and archives (zip, rar, 7z,
tar, read in place and **never unpacked into your library**) just work. Music nothing recognises becomes
a visible *unverified* album instead of sitting in an import screen for ever.

### TV and anime the way Sonarr does it, and more

Sonarr's add dialog, monitor options, naming tokens, Wanted and Cutoff unmet, series types and season
folders, plus:

- **Other orderings**: number a series by TheTVDB's DVD, absolute or alternate order, previewed first.
- **Air times**: an episode counts as out only once it has aired in its own time zone.
- **Crossovers**: events across series in watch order, and one broadcast held once and linked into both
  series' folders.
- **Franchises and related series** from Wikidata and AniList (sequels, spin-offs, the whole universe).
- **Jellyfin and Kodi files** (optional): nfo files with the provider IDs, and artwork, written only where
  none exists.

### Movies the way Radarr does it, and more

Radarr's naming, editions, quality and search, plus **collections and franchises beyond TMDB's** (a
universe across several TMDB collections), a per-library **rating limit** (a kids' library never
searches above its age), and import of your Radarr setup.

### Libraries that span many disks

A library can have any number of root folders. An artist or series stays on one disk, new content goes
where there's room, and **retiring a disk is one button**: Solarr previews the move, checks the space
and drains it. A folder can be read-only, or declared offline for good, without anything on it being
treated as missing and re-downloaded. Every move is one engine: copy, verify, repoint, then delete.

### One copy of the same file

Two libraries (or two series) that want the same file share **one copy**, linked with a **reflink, else
a hardlink**, after showing you a preview. A separate report lists likely duplicates by metadata and
recommends the copy closer to your target quality.

### Quality that means it

The **file is the evidence, not the release name.** Solarr measures what it downloaded with ffprobe, so
fake upgrades are refused, ties keep what you have, and a bigger number doesn't automatically win
(192 kHz does not beat a clean 48 kHz just for being larger). Profiles cover formats, codecs, bit depth,
resolution, HDR, audio and subtitle languages, and preferred release groups. **Remove and block**
recycles a bad file, blocks its exact bytes (renaming it can't sneak it back) and wants the item again.

### Easy to run, easy to use

Everything is done from the browser, and settings come in **normal / advanced / expert** levels so you
only see what you need. Several users can have different access to different libraries. Every decision
keeps its reasons, so "why did it do that?" is answered on the page, not in a log file. Filters, sorts
and views are remembered per user.

### Move over without starting again

Solarr reads your existing **Lidarr**, **Sonarr**, **Radarr** and **Prowlarr** setup (only ever *reads*,
never changes it). Naming defaults match theirs, so adopting an existing library proposes **no renames**.

### Side by side

| | Lidarr / Sonarr / Radarr | Solarr |
|---|---|---|
| Media types | One per app | Music, TV, anime and movies in one app |
| Libraries | One per instance | Any number, each with its own folders, quality, users and rating limit; mixed types allowed |
| Searching | You press *Search all* | A planner hunts within a budget, plus RSS and upgrade hunting |
| Full download client | Grabs fail or wait | Held releases, sent in priority order as the client frees up |
| Import | Sorted folders, one at a time | Any folder, any depth, previewed, fully revertible |
| Packs and box sets | Weak | One download → many albums or seasons |
| Archives | Extracted | Read in place |
| Unrecognised music | Stuck in the import screen | Visible as an unverified album |
| Fakes | Blocklist by release name | Blocks the exact bytes and wants the item again |
| Quality | Ordered format list | Gates, tiers and a score, judged from the real file; ties keep yours |
| Why did it do that? | A rejection string | Structured reasons kept per decision |
| Metadata | One source per app | Several per library, in an order you choose |
| Several disks | Root folders | Affinity, read-only / offline / draining, one-button drain |
| Same file twice | Two copies | One copy, linked (reflink or hardlink) after a preview |
| Crossover episodes | One series only | Held once, placed in both series |
| Franchises | TMDB collections (movies) | Collections plus Wikidata franchises for movies and series |
| Deleting | Optional recycle bin | Always recycled first |
| Users | One login | Several, with per-library access |
| Settings | One long list | Normal / advanced / expert |
| Deployment | .NET app per media type | One Go binary, four small SQLite databases |
| Notifications | Many connectors | In-app centre only for now |
| **Maturity** | Years in production | **Early alpha** |

---

## Metadata providers

The \*Arrs each lean on one metadata service. Solarr asks **several providers per library type, in an
order you choose per library**: the first provider that knows a field supplies it, each later one only
fills what is still empty, and any provider can be switched off.

| Library | Providers |
|---|---|
| **Music** | MusicBrainz, BrainzMash (a fast MusicBrainz mirror), Lidarr's metadata service, Cover Art Archive, TheAudioDB, Wikidata and Wikipedia, AcoustID fingerprints |
| **TV and anime** | Skyhook (Sonarr's service), TheTVDB, TMDB, TVmaze (air times with time zones), AniList (anime titles and relations), Wikidata |
| **Movies** | TMDB, Radarr's metadata service, Wikidata, TheTVDB, MDBList and OMDb (ratings), fanart.tv (logos and art) |

**Keyless by default.** Every library works out of the box with free, keyless sources; TMDB, fanart.tv,
MDBList and OMDb are added when you enter your own free key.

**Wikidata ties them together.** Wikidata knows the ID every other provider uses for a work, so Solarr
uses it to bridge providers (a TMDB film to its IMDb and TheTVDB entries), to fix IDs that disagree, and
for franchises no single provider holds.

**Fast enrichment.** Enrichment is built to never be the bottleneck:

- **Per-provider rate limits**: each provider has its own pace (its published limit, adjustable live in
  the interface), so a slow provider never holds up a fast one.
- **Bulk reads**: Wikidata is asked about 40 works per query instead of one at a time. Importing about
  4,000 films from Radarr went from a projected **33 hours to 29 minutes** when per-film lookups were
  replaced by bulk reads.
- **Parallel workers** per media type, a shared response cache, and refreshes that only re-read what
  changed.
- **Artwork arrives in the background**: a library is usable straight after the scan, covers and photos
  fill in over the next minutes.

The full table, with what each provider carries, is in
[metadata providers](documentation/metadata-providers.md).

---

## Speed and performance on large libraries

Solarr is designed for **250,000+ files** and tested against 500,000. Numbers below are measured on the
developer's machine, not guarantees.

### How the \*Arrs behave by default, and what Solarr does instead

| Situation | \*Arr default behaviour | Solarr |
|---|---|---|
| Opening the library | The whole library is loaded into the browser, then filtered there; big libraries mean long waits and a heavy tab | The server pages and filters; the browser holds one page, lists are virtualised. **Page 1,000 costs the same as page 1** |
| Counting things (dashboard, sidebar) | Computed from the full list | Counters kept up to date as things change: a number is a lookup, never a `COUNT(*)` |
| Periodic refresh | A scheduled refresh re-reads metadata and rescans folders item by item | Only what changed is looked at again; providers are read in bulk |
| Finding what's missing | *Search all* fires a search per item | A planner reads the database (no disk I/O) and hunts the most urgent items within an hourly budget |
| A big write (scan, import) | Can stall the interface or hit "database is locked" | WAL-mode SQLite, one writer behind a priority queue: **your click jumps ahead of a bulk scan** |

### Measured

**First scan of a real music library.** About 200,000 audio files on an NFS NAS, scanned and identified
from an empty database: **99.9% identified**, in roughly **1 to 3.5 hours** depending on NAS load. The
bottleneck is the NAS, not Solarr. An interrupted scan resumes where it stopped.

**Importing from the \*Arrs.** A full Sonarr (about 1,000 series) and Radarr (about 4,000 films) import,
run side by side: **38 minutes** for the series, **29 minutes** for the films, every item imported.

**The database, on a synthetic 500,000-file library:**

| Operation | Typical (p50) | Worst case (p99) | Target |
|---|---|---|---|
| A page of the library list, filtered | 1.7 ms | 3.5 ms | 100 ms |
| Search-as-you-type over titles | n/a | 17 ms | 150 ms |
| Import 200 rows | 3.7 ms | 5.7 ms | 50 ms |
| A user's edit while a bulk write runs | 4.3 ms | 19 ms | 25 ms |

A TV library of about 1,000 series and 85,000 episodes answers every list in under 0.25 s. These
targets are tests: CI fails if one is missed (`make bench`). Details: [benchmarks](documentation/benchmarks.md).

---

## Design decisions behind speed, extensibility and usability

**Speed**

- **One static Go binary**, interface embedded. No .NET runtime, no external database, no broker.
- **Four small SQLite databases instead of one large one**, each with one writer: `settings.db`
  (configuration, fully cached in memory), `library.db` (everything that scales with your media),
  `jobs.db` (the work queue, high churn, kept apart) and `statistics.db` (history and figures; the
  library stays correct with it empty). Details: [database layout](documentation/database-layout.md).
- **Short transactions, bounded batches.** No transaction over about 50 ms, bulk work in 200-row
  chunks, import concurrency of one. Interactive work never waits long behind background work.
- **A pure planner.** The planner reads state and emits idempotent actions; it does no I/O, so it can run
  often and cheaply, and running it twice does nothing twice.
- **Measured, not claimed.** Latency targets are enforced in CI.

**Extensibility**

- **One generic core, media types as declarations.** Music, TV and movies are not three apps glued
  together: they are one engine (identity, quality, search, import, moves) with each media type plugged
  in through named extension points. Adding a type means declaring it, not forking the core.
- **Spec-first API.** The OpenAPI document generates both the Go server interface and the TypeScript
  client, so every screen is built on the same public API you can script against.
- **Structured decisions.** Rejections and score components are data, not strings, so the interface,
  the API and future automations all read the same reasons.
- **Every periodic job is registered** in one place, visible and runnable on demand from the interface.

**Usability**

- **Everything from the web interface.** `config.yml` only holds bootstrap values; everything else is
  set in the browser, and a read screen always comes with its write screen.
- **Previews before anything irreversible** (imports, renames, moves, drains, links), and **undo** after.
- **Nothing deleted directly**: one move engine (copy, verify, repoint, unlink) and a recycle bin.
- **It explains itself**: every search, grab, refusal and upgrade keeps its reasons on the page.
- **Familiar where it should be**: Lidarr's, Sonarr's and Radarr's layouts, naming tokens and add
  dialogs, so moving over feels like home.

---

## Privacy

Solarr is **privacy-first and localhost-first**.

- **No telemetry, no analytics, no crash reporting, no accounts, no update check.**
- It contacts **only what you configure**: the metadata providers enabled for your libraries, your
  indexers, your download clients and, if you use the import, your \*Arr instances.
- The one optional extra, a check of your public IP (to see whether traffic leaves past a VPN), is **off
  by default** (`network.public_ip_check` in `config.yml`).
- Secrets (API keys, passwords) are **encrypted at rest** and never returned by the API.
- Every outbound request goes through one HTTP client with a destination guard, and can be routed
  through a proxy.

**Don't take our word for it.** The code is open: point your own AI (or your own eyes) at it and ask
where it connects to. Everything outbound passes through `internal/platform/httpx`.

---

## Installation

Solarr ships as one Docker image for **amd64 and arm64**, with everything it needs inside (`ffprobe`,
`fpcalc`, the tag writer). Images: [`makearr/solarr`](https://hub.docker.com/r/makearr/solarr) on
Docker Hub and `ghcr.io/makearr/solarr` on GitHub.

```yaml
# docker-compose.yml
services:
  solarr:
    image: makearr/solarr:latest
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
      - /media:/media           # your library (add :ro while you try it out)
      - /downloads:/downloads   # the same path your download client reports
```

Then open **http://localhost:5454**. There's also a ready-made [docker-compose.yml](docker-compose.yml).

Worth knowing:

- **`PUID`/`PGID`** set who Solarr runs as. It takes ownership of its own folders only, never your media.
  Don't set `user:`.
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

Versions are `YYYY.MM.RRR.BBB`: year and month of the release, the release number, and a build number
that advances with every build. See [versioning](documentation/versioning.md).

---

## First steps

The full walk-through is the [user guide](documentation/user-guide.md). In short:

1. **Setup wizard:** create the administrator and your first library (a name, a type and a folder).
   Solarr scans it; covers, posters and photos arrive over the next few minutes.
2. **Connect an indexer and a download client** (SABnzbd or qBittorrent) under *Connections*, or take
   them from Prowlarr with *Connections → Arr*. The dashboard tells you what's still missing.
3. **Add music, series or movies** with *Add new*, or import your Lidarr, Sonarr or Radarr setup.
4. **Let it run.** Solarr checks RSS and searches for what's missing on a schedule.
5. **Follow along** in *Activity → Queue / History* and on each work's page, which says when it was last
   searched, when it will be again, or why it won't be.

---

## What works today, and what doesn't yet

| | |
|---|---|
| **Libraries** | Music, TV, anime and movie libraries, mixed libraries · poster, overview and table views · saved filters · bulk edit · tags with a genre tree · rating limits · calendar with a subscribable link |
| **Music** | Artists, albums and editions · labels · aliases and merges · add with monitoring · Navidrome-friendly tags and covers (optional) |
| **TV and anime** | Series, seasons, episodes, daily shows, absolute numbering, other orderings · cast, crew, networks, follow · crossovers · franchises · Jellyfin/Kodi files (optional) |
| **Movies** | Add, scan, search, grab, import, rename · editions · collections and franchises · Radarr import |
| **Finding** | Newznab/Torznab indexers (Prowlarr works as-is), in parallel · hunting, RSS, upgrade hunting · manual search with scores and reasons · delay profile |
| **Downloading** | **SABnzbd** and **qBittorrent** · queue, history, held releases, free-space protection · seeding limit per client · failed releases blocklisted and searched again |
| **Running it** | Web UI · several users · notifications centre · statistics · encrypted secrets · automatic backups · scheduled tasks you can run on demand · YAML settings export |

**Not there yet:** torrents work end to end but have seen less use than usenet · notifications are
in-app only (no webhooks or connectors yet) · external import lists (Trakt and others) · matching the
same audio across formats for duplicates · books, audiobooks and comics come later. Not API-compatible
with the \*Arrs, on purpose: their data model can't express editions, packs or files shared between
libraries.

---

## Built with AI (and a lot of patience)

Let's be honest up front: **Solarr would not exist without AI.** It was written with
[Claude Code](https://claude.com/claude-code), mostly **Claude Opus on a Pro plan**, by one person with a
day job, a large media library and strong opinions about how the \*Arrs should have handled box sets.

The division of labour, roughly:

- **The human** wrote the brief, made the decisions, tested everything against a real library, filed the
  bug reports, and repeatedly asked *"did you actually look at it, or did the tests just pass?"*
- **Claude** wrote the code (about 270,000 lines of Go, 50,000 of TypeScript, in about two months),
  the tests, the migrations, the documentation and, at one point, roughly 2,000 lines of comments
  nobody asked for, which it then also had to delete.
- **The usage limit** decided when everyone went to bed.

A one-person hobby project does not get a generic media engine, eleven planned media types, a
desired-state planner, four databases, a recognition pipeline and a test suite with latency budgets.
With AI, it does. The ideas, the taste and the stubbornness are human; the typing is very much not.

### Supporting the project

The most useful thing you can donate is **compute**: a **Claude gift subscription** goes straight into
building Solarr. Get in touch through [GitHub](https://github.com/makearr) if you'd like to.

Bug reports are just as valuable, and free (see [Early alpha](#️-early-alpha)).

### Contributing

Pull requests are welcome. Solarr does have firm core values (one generic core, nothing deleted directly,
everything from the web interface, privacy first, the file as evidence), and I reserve the right not to
merge a change that works against them, however good the code. **If you want to build a feature, please
open an issue and ask first**, so your time isn't wasted.

Start at [codebase-map.md](documentation/codebase-map.md) and
[coding-standards.md](documentation/coding-standards.md); `make verify` must pass.

---

## More

- **Documentation:** [documentation/](documentation/README.md): [user guide](documentation/user-guide.md),
  [music identification](documentation/music-identification.md), [TV internals](documentation/tv-internals.md),
  [naming](documentation/naming.md), [downloads](documentation/downloads.md),
  [file management](documentation/file-management.md), [container runtime](documentation/container-runtime.md).
- **Third-party reuse:** [ATTRIBUTION.md](ATTRIBUTION.md). **History:** [CHANGELOG.md](CHANGELOG.md).

## Licence

[AGPL-3.0-or-later](LICENSE). Free to use, modify and self-host. If you run a modified version as a
service for others, you must make your changes available to them.
