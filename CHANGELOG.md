# Changelog

Version is `YYYY.MM.R.BBBB` (year.month.release.build) from 2026-10-31 (`YYYY.MM.RRR.BBB` before); see `documentation/versioning.md`.
Earlier entries use older schemes; those at and below `0.0.x` predate both and are left as history, unconverted.

## Unreleased
- Discover: a sidebar page (poster wall per library with list, genre and title filters, Add and Not interested) and a dashboard row of recommendations.
- Discover (settings): Settings > General has the switch; Settings > Discover sets what is never recommended and, per library, list sizes, language, rating and vote floors and automatic adding.
- Discover (sources): audiobook libraries recommend from Audible (best sellers, highest rated, upcoming) and adult libraries from StashDB (trending, upcoming) once its key is set.
- Discover (settings): a library with more than one kind of item (artists and albums) can switch each kind off, and Refresh now reads the lists again without saving.
- Discover (fixes): the sidebar entry sits below Calendar, the dashboard row no longer clips its list badges, a plain ?library=2 or ?hidden=1 link opens that library or the Hidden view, and a card without a poster shows its initial.
- Discover (API): the recommendations of each library, a dashboard row, genres, per-person dismissal and undo, and Load more reading the next page from the source.
- Transcode (settings, not yet active): per-target conversion setups, per-library switches and lane limits can be read and saved through the API and are part of the configuration export; nothing is converted yet.
- Discover (page): the poster wall is virtualised, Not interested offers Undo, a Hidden view lists what you hid with Restore, a work you hold but do not monitor gets Monitor, request-only accounts see Request, and Settings > Discover has word chips for exclusions and shows when each list was last read and why it failed.
- Discover replaces the TMDB trending and popular import lists, and the unused discovery jobs are gone. On upgrade a library that had such a list gets Discover switched on with Trending adding automatically when the list was enabled (popular becomes trending; the rating and vote floors now apply), the lists are deleted, and this cannot be undone.
- Discover: music libraries recommend artists and albums from Last.fm (needs your Last.fm key): what is trending, and what is alike the artists and genres the library holds most of; Last.fm has no ratings (that gate is skipped) and gives albums no listener count, so automatic adding of albums needs the vote floor at 0.
- Discover: ebook libraries recommend from Open Library (trending this week, best rated, classics); comic and manga libraries read AniList's manga charts; audiobooks and adult libraries have no feeds yet.
- Discover: anime libraries read AniList charts (trending, top rated, upcoming, classics) instead of TMDB.
- Discover: automatic adding. After each refresh a library adds recommendations that pass its rating and vote floors (most popular first, up to the daily limit, never one it added before); switching it on for a list first shows what it would add now and why it holds the rest back.
- Discover: an instance switch (Settings > General) and per-library settings (list sizes, rating and vote floors, exclusions, daily auto-add) for recommendations, saved from the API and carried in the configuration export and import; the recommendations themselves are refreshed daily.
- Comics: an issue that arrives as a folder of page images (jpg, png, webp, avif, jxl, gif; at least three, cover first, natural order) is packed into one cbz and imported as that file; a download's folder is left as it was, a folder you imported by hand goes to the recycle bin once the cbz is in the library.
- Metadata language: one setting under Metadata providers (default English) makes TMDB, Skyhook and Wikipedia answer in your language; an overview comes from that language's Wikipedia article (found through Wikidata) where the library's order lets Wikidata supply it, and English is used where a provider has nothing in your language.
- TV: the anime id map (Fribb) is now on by default; switch it off under Metadata providers.
- Refresh now works on a series a scan added that has no episodes yet (it answered "not found"), and a provider definition that answers a JSON list fills genres and tags from its elements.
- Work page: a series, film, book, comic or scene whose metadata so far came only from your own media server shows a "From your media server" chip; it goes once another metadata provider of its library answers a refresh (music never carries it: albums always come from MusicBrainz).
- Comics: a creator (writer, artist, ...) can be followed; their series at ComicVine are added monitored, new ones from the year of the follow on (needs a ComicVine key; not yet tried against the live service).
- Import: Kapowarr and Mylar3 can be connected like an *Arr and their comic series imported by ComicVine id (monitored or paused/ended, placed by the folder they live in) -- built from their public API, not yet tried against a real instance. Schema note: the connection table is rebuilt in the unreleased settings migration.
- Comics and books: Kavita can be a local metadata source (series summary, genres, credits, publisher and the provider ids in its links), ranked after every online provider and only filling what they lack.
- Books: BookBrainz is offered as a metadata provider (works, authors, description, language; keyless, not in the default order).
- Music: an optional per-library Lyrics setting writes synced (.lrc) or plain (.txt) lyrics from LRCLIB beside tracks that have none, once a day, never over a file you put there.
- Webhooks: a webhook with an invalid address or event now answers a clear 400 naming the problem instead of an internal error.
- TV: an anime id map (Fribb, one file read once a day) finds an anime's AniDB, MyAnimeList and AniList ids from its TheTVDB id.
- Music: the cover picker also offers Apple Music's large cover for an album with a barcode (keyless; a switch under Metadata providers).
- Media servers: Stash is told of new files (a metadata scan of their folders); Emby and Komga can be connected (scan push; Emby also metadata and watch state) -- both built from their public API, not yet tried against a real server.
- Your own provider definition files can now enrich music artists (photo and biography where none) and fill adult-library scenes (title, studio, tags, pictures, credits).
- Comics: a series known only to the Grand Comics Database now gets its issue list (variants left out, up to 60 issues a refresh, on the provider's own pace).
- Your own provider definition files can now fill a TV library's series (title, overview, genres, rating, pictures, cast; never episodes) when ordered in its sources.
- TV: each episode row shows its own IMDb score (read from OMDb's season lists when an OMDb key is set; one request per season on a refresh). Library database gains `rating` and `votes` on episodes.
- TV and movie pickers offer your Jellyfin server's posters and backdrops (never as the default over an online source); they are fetched with the connection's own sign-in and cached.
- Configuration export and import carry your webhooks (outgoing and incoming) and your own provider definition files, never an address, a secret or a token; an imported webhook starts switched off until its address is set.
- Manga: a volume release is no longer turned away because its edition year (a reprint) differs from the series' first year.
- Comics: a release named "Complete" or "Collection" counts for every part still wanted; history and queue rows say what a comic release holds (#12, v03, c120, Complete).
- Books and audiobooks: a scan names a book by the ISBN or ASIN your Audiobookshelf holds for its folder when the file name and tags give none.
- New library and the Also holds list: media types are listed by their own name (TV and Comics were both "Series").
- Indexers: the add list marks an indexer defined by your own spec file ("Your own file") and a shipped one your file replaced.
- A part wanted on its own (one volume, one track) now starts with the file the library already holds for it, and starts with its media type's quality profile when the library has none of its own (a manga volume got the music profile).
- Comics: a volume or issue found by a scan now also counts for the item that wants that volume on its own (it stayed missing, so a cutoff never compared it); the old bytes of a cbz that gets ComicInfo.xml written go to the recycle bin first; an import triggers the Kavita and Komga files pass for its series.
- TV: a series page shows every provider's score as its own chip (OMDb's IMDb rating included) instead of only the first source's.
- Download client definitions can sign in with a login request (the session cookie is kept) and read XML and HTML answers, not only JSON.
- Comics: an edition page has a Covers dialog to say which issues a collected edition holds (a series and a range), the library list filters by Format (the Trades view), and the Kavita and Komga files setting counts how many files it would write before it is switched on.
- Watched: the library tree shows how many works are ready to clean up beside the Watched entry (refreshed by the watch-state sync); a non-administrator who opens the page is told it is administrator-only instead of an error.
- Webhooks: `file_recycled` now fires when a file goes to the recycle bin (it never did for the bin itself).
- Comics: an issue held in a collected edition shows "In the collected edition" with a link to it on the series page.
- Webhooks: the form shows the body of the chosen format for an example event, and `GET /webhooks/{id}/pending` lists what still waits for a webhook (event, attempts, next try).
- Music scan: an album folder whose files carry no MusicBrainz tags is named by the ids your Jellyfin server holds for it (before any title search); the identification detail shows the pass.
- Download clients as files: a definition file (`engine: http`, usenet) describes how to talk to a client's HTTP API; once approved in Settings > Providers it is offered beside the built-in clients. Solarr still implements no client itself.
- Webhooks out (API): an administrator adds webhooks with a preset (generic JSON, Discord, Slack, ntfy, Gotify, Apprise), the events they take and the libraries; deliveries go through an outbox with backing-off retries and an HMAC signature, and the address and secret are sealed. Schema note: new settings and library tables (dev databases need recreating).
- Metadata providers as files: e-Hentai, ExHentai (your own session cookies) and nhentai (experimental, through your own FlareSolverr) ship switched off as definition files and are offered to Hentai libraries only; two ports of compiled clients prove the engine against the Go code.
- Your own metadata providers (definition files) now also work for books, audiobooks and movies; adult-only definitions are offered to adult libraries only; their answers are cached as the file says. A custom descriptor can narrow a provider's media types, name indexer mirrors and set a request delay.
- Fixed: the library Metadata page failed to load once a custom provider without a feature list was offered; a custom provider now shows its Custom chip in the unused list too, and a provider switched off by a custom descriptor no longer appears in Settings > Metadata.
- Connections > Webhooks: add, edit, test and remove outgoing webhooks (events as grouped checkboxes, libraries, file paths, adult libraries) and incoming webhooks (a token shown once, curl lines and the Jellyfin Webhook plugin fields for scan, import, refresh, watch state and search). Outgoing webhooks now also tell of grabs, failed downloads and imports, renames, additions and removals, requests and health changes. Schema note: new settings table (dev databases need recreating).
- Settings > Providers: list every provider definition (metadata, indexers, download clients, webhook presets) with its origin and state, edit a custom file with its problems listed by line, copy a built-in one to custom, approve the hosts of a custom provider before it runs, test a metadata provider, and revert or remove a file (it moves to custom/.removed).
- Provider definitions: an active metadata definition file now joins a comic library's provider order (picker marked Custom); a definition that is gone or broken shows as no longer available and is skipped.
- Provider definitions: the engine behind a metadata provider that is only a YAML file (JSON, HTML or XML answers read into one generic record, at most eight requests an operation, only the declared hosts). It is not offered to libraries yet.
- Provider descriptors: every built-in metadata provider, indexer kind and download client is written out as a YAML descriptor under `providers/`; a custom file with the same key can switch it off for new use (hides it from the add lists and a library's provider list), lower its request pace and narrow its media types.
- Indexers: the built-in indexer specs are written out as YAML under `providers/indexers/builtin/` in the config folder, and a custom spec in `providers/indexers/custom/` (same key replaces a built-in) is used once an administrator has approved its hosts.
- Picture pickers: a series or movie also offers its fanart.tv logos and banners; an album also offers its Deezer cover (found by barcode when a release has one). Books: an Open Library match that names an Internet Archive item reads that item directly.
- Music: the album and artist image pickers also offer fanart.tv covers, disc art, artist photos, backgrounds, logos and banners (when fanart.tv is on and has a key).
- Fixed: Deezer ratings missed albums whose plain edition is not among the first search hits; a local media server's score now reads as the server's name on a work.

- Calendar: the Pull list (and the calendar's dated parts in general) now lists issues and volumes of comic libraries; it showed only TV episodes before. A release whose size cannot be its named parts (a stub, a bundle) is now rejected with its reason instead of silently dropped.
- Comics: a file holding a range of volumes is named for both ends (v04-05), and number padding pads both.
- Comics: a manga or hentai library says volumes where a comics library says issues, and a comic file shows its page count, median page size and image format beside its size.
- Comics: a library can write what Kavita and Komga read (File Management > Library options > Kavita and Komga files): series.json beside each series and ComicInfo.xml inside each cbz, adding what is missing (fill) or replacing it. A file is rewritten only when the result differs. Off by default.
- Comics: a library chooses its date type (store date or cover date) and whether a collected edition it holds satisfies the issues it collects (those are then not searched for and show as held in the edition); a ComicRack reading list (.cbl) can be imported on the Story arcs page: matched issues are kept in order as one list. Libraries gained a generic "Library options" section for settings a media type declares.
- Comics: a download of cbz/cbr files is no longer unpacked into its pages before import; a Manga library names its series folders without a year and starts on the Manga default target.
- Calendar: a Pull list view (comic libraries only, weeks starting on Wednesday).
- Comics: four shipped CBZ quality targets (any page size, SD, HD, manga HD) with a page size the median page of a file must reach (a file whose pages were not measured is never held against it), comic quality profiles (CBZ preferred, Any comic, High resolution), a Pages section in the quality target editor, and Rename now names comic files by the library's naming scheme. The "also the specials" box shows only for TV.
- Comics: wanted issues, annuals, volumes and chapters are searched (Newznab comic and book categories) with the series title and the number as releases print it, in the hunt, the RSS sync and interactive search; a release must name the series and a wanted part, alone or in a range; the cutoff check keeps the better file of a part.
- Comics: downloads of comics and manga are imported (issues, volumes and chapters named by file, folder or ComicInfo, ranges, placed below the series' folder and named by the library's naming scheme), files waiting in Review can be assigned to a series, and a series you removed with "do not add it back" is no longer re-added by the next scan.
- Metadata: MyAnimeList and AniDB can add an anime's score once you store a client id or client name under Settings > Metadata (both off by default).
- Metadata: Last.fm, Discogs and Spotify can add an album's popularity or rating once you store your own key under Settings > Metadata (all off by default). Jellyfin's people become cast and crew of a series or film when no other provider has any, and a series keeps the TheTVDB id its folder names.
- Metadata: ListenBrainz and Deezer add how popular an album is (listeners and fans, shown as a rating beside MusicBrainz's) when a music library's albums are enriched; both need no key and can be switched off under Settings > Metadata.
- Metadata: IMDb ratings from IMDb's own daily download (switched off; no key, no quota, personal and non-commercial use) fill the rating of films and series, and outrank the IMDb number MDBList and OMDb give; switch it on under Settings > Metadata and Run now under Scheduler.
- Media servers: a connection a library's metadata order still names can no longer be deleted by mistake, and connecting a media server as a metadata source takes effect at once.
- Comics: a new media type with four library kinds (Comics, Manga, Adult comics, Hentai) that can be created and are empty for now; the two adult kinds ask for a confirmation before the library is added. Under the hood: the file-name parser, ComicInfo.xml / MetronInfo.xml / series.json readers, the page probe and clients for ComicVine, Metron, the Grand Comics Database, MangaUpdates, MangaDex and AniList manga are built but not yet connected.
- Comics: ComicVine, Metron, the Grand Comics Database, MangaUpdates and MangaDex appear under Settings > Metadata (switched off until you turn them on; ComicVine takes your key, Metron your account as user:password); AniList also serves comic libraries. Comics libraries default to ComicVine, Metron, GCD; Manga and Hentai to AniList, MangaUpdates, MangaDex.
- Comics: Add series in a Comics, Manga, Adult comics or Hentai library: search the providers of the library, pick Sonarr-style monitor options (all, future, missing, first, latest, none), series and their issues or volumes are stored, and a daily refresh keeps ongoing series current. Manga volumes come from MangaDex's chapter map or the volume count AniList and MangaUpdates give; chapters outside any volume are listed but not wanted. A result an adult-flagged provider marks is offered only to the adult kinds.
- Comics: a scan of a Comics, Manga, Adult comics or Hentai library now reads each archive (cbz, cbr, cb7, cbt, pdf: page count, page size, image format, embedded ComicInfo.xml / MetronInfo.xml), finds the series (an id in the file first, then its name and folders, then the providers; added unmonitored when sure, else queued in Review with candidates), and records which issues, volumes or chapters each file covers, including ranges such as 001-006 and v01-v03. A chapter file counts toward the volume MangaDex lists it in; a chapter no volume holds stands alone.
- Comics: a series page groups its parts as Issues, Annuals, Specials, Volumes and "Chapters not yet in a volume", names untitled parts ("Volume 3", "Issue #1"), and a manga whose volume count no provider states gains the volumes its files name. See documentation/comics.md.
- Comics: adding a manga or comic whose parts no provider lists yet still puts it in its library; MangaDex's "erotica" rating (given to much mainstream manga) no longer counts as adult, only "pornographic" does; the add dialog shows the type's monitor options.
- Development moves to branch `dev`; `main` on GitHub stays at the last release.
- Metadata: OMDb (IMDb rating, English plot) and fanart.tv (clear logos, banners, posters) now also serve TV libraries, last in the default order; media servers used as metadata will rank after every other provider.
- Metadata: a Jellyfin connection used as metadata fills a TV or film library's missing fields (overview, genres, cast, age rating, ids) and names files and folders it already knows during a scan; it ranks after every other provider and never replaces an id.
- Metadata: an Audiobookshelf, Calibre or Stash connection used as metadata fills a book, audiobook or adult library's missing fields (found by ASIN, ISBN, Calibre identifier or stash-box id, else by exact title); ranked after every other provider, never replacing an id.
- Settings > Metadata lists the media servers used as a source of metadata (kind, libraries, state, link to the connection); a library's Metadata page marks such a provider and says it is asked first and replaced by the others where they know better.
- Metadata: the Internet Archive fills a book's missing description, subjects and cover by ISBN, last in the book order (no key).
- Metadata: Kitsu (no key) adds anime synopsis, age rating, average rating, poster and the MyAnimeList and AniDB ids, after the other providers in TV libraries.
- Connections > Media servers: connect Jellyfin, Navidrome, Audiobookshelf, Kavita, a Calibre content server or Stash with a tested address and credential, and map Solarr libraries and paths to the server's. Settings schema 0078 adds the connection tables. Telling servers about new files, local metadata and watch state follow.
- Media servers: Jellyfin, Kavita and Stash sign in with a user name and password as well as an API key (Jellyfin may have an empty password); the session is kept and renewed when the server refuses it.

- Media servers: after Solarr places, moves, renames or recycles files, connected Jellyfin, Audiobookshelf, Kavita and Navidrome servers are told (targeted where the server can, one library scan otherwise); Jellyfin can also show "Now available" to people watching. Library schema 0147 adds the queue.
- Media servers: watch state is read from Jellyfin, Audiobookshelf and Navidrome every 30 minutes (or on demand) and kept by work and part. Work tiles and rows show a check or an eye, the work list filters by watched, in progress or not watched, and Libraries > Watched lists what everyone has played so its files can be moved to the recycle bin (optionally unmonitoring the works).

## 2026.11.2.1311 — 2026-11-22

**Upgrading:** each database is one squashed baseline again. Only databases from build 1298 or later (schema
library 0146, settings 0077, jobs 0010, statistics 0008) are upgraded. Anything older, **2026.11.1.0882 included**, is
refused at start-up: use the *Arr import or start with a fresh config folder. The automatic move of the old
`data/{db,cache,backups}` layout and the old browser theme setting are gone too.

- **Indexers without Prowlarr.** Our own indexer spec engine with a **581-site catalog**, a proxy list with
  FlareSolverr, indexer priority and per-indexer download client binding, categories read from the indexer itself,
  and stated API allowances respected (back-off on a limit answer, one sign-in shared by parallel searches).
  Prowlarr can still be followed: its indexers, proxies and clients sync hourly.
- **Disk-aware I/O.** Settings → Disk shows the disk topology and latency; probing, tag write-back, moves, linking and
  imports wait their turn per disk, idle disks are left alone, and inotify folder watching lets scans trust it.
- **Scheduler.** Pause and resume any task (or all), a *Running now* list with time left and estimated load, a
  heatmap of real run time, a hung-job watchdog, and a task that missed its time while Solarr was down runs once.
- **Imports.** Blu-ray/DVD folders are one unit held for review; Calibre `metadata.opf` and EPUB metadata name a book;
  audiobook tags lead the folder name; disc subfolders, subtitle folders and hidden pregap tracks are handled;
  every failing step takes back what it placed; the Import screen pages, has a *Needs attention* tab and dismisses
  all finished sessions in one click. How long import history is kept is a setting.
- **Music.** Bootleg-only release groups per library, optional tracks, release detail (edition names, disc titles,
  track artists, ISRCs), duplicates by **audio fingerprint**, community ratings, vote-weighted genres, Lidarr import
  takes over naming formats and metadata profiles.
- **TV.** Daily series searched by air date, a season missing a few episodes searched episode by episode, several
  downloads of one season in parallel when they do not overlap, specials leave the missing count, and seasons that two
  sources number differently are shown on the work page.
- **Downloads.** A title can be wanted by two libraries; a dead `.torrent` falls back to its magnet; BitTorrent v2
  hashes; a full client disk keeps downloads waiting instead of blocklisting them; a release an indexer stopped
  serving is blocklisted as *gone at indexer*; renamed and blackhole downloads are still recognised; a removed
  download is only declared gone after ten minutes.
- **Similar videos** are compared by a nightly job and stored, so the report opens at once; a pair is compared only
  when their lengths agree.
- **Safety.** Deleting anything in use is refused or offers to move its users first; a fresh install claimed from a
  public address needs the setup code from the log; access levels per action (add, manage) in one table; files of a
  blocked work are a 404 everywhere; passwords of 8+ characters asked twice; credentials never follow a redirect to
  another host; API writes trigger a database copy to disk when running from RAM.
- **Faster and lighter.** A metadata refresh that finds nothing changed writes nothing; library covers sit in 256
  shard folders; one event stream per tab; JSON answers gzipped; provider outages serve the stored answer;
  backups and vacuum no longer hold the database writer; the job dispatcher no longer polls.
- **Health** names unreadable files (with ffprobe's message), missing media tools, failing tasks, stale backups,
  disabled clients still holding downloads and a missing AcoustID key.
- **The whole interface is translatable**: every sentence comes from one i18n catalogue (English only for now).


## 2026.11.1.0882 — 2026-11-08

**Upgrading:** databases created by 2026.10.001.515 or earlier are refused at start-up (one baseline migration per database now); import from the *Arrs or start a fresh config folder.

- **Books and audiobooks are real libraries.** Metadata from Open Library, Google Books, Hardcover, FantLab, iTunes and
  Audible (with Audnexus), covers, one collection per series and the books a series still lacks, **Monitor** on a
  whole series, **follow an author**, and a **Narrators** level, filter and column for audiobooks.
- **An adult library type** (optional) over the stash-box providers, ThePornDB and Whisparr's metadata: scenes and
  films, performers, and a StashDB favourites import list. See [adult](documentation/adult.md).
- **Import lists for TV.** An AniList season or user list, a TMDB list or TMDB's trending and popular shows add series
  for you, **previewed before you enable them**. See [import lists](documentation/import-lists.md).
- **Import from the \*Arrs by choice.** A preview and mapping step, then pick what to bring across; Whisparr is a
  source too, and monitoring can be **sent back** to the \*Arr.
- **Merge one library into another** of the same media type, previewed like a move.
- **Favourites.** A per-user heart on works, parts, people, organisations and collections, with a Favourites filter.
- **A preview player in the browser**: bar, corner window or full screen, source bitrate, quality menu and **GPU
  transcoding**. See [media player](documentation/media-player.md).
- **Similar videos report** across one library or all: perceptual hashes of the frames plus duration, keep one and
  link or recycle the others. Hashing is **off by default**. See [duplicate videos](documentation/duplicate-videos.md).
- **More download clients and indexers**: every client Lidarr and Sonarr offer, **Soulseek through slskd**, and
  **Prowlarr's catalog** to choose indexers from.
- **Download queue insight**: the dashboard says when the queue will be empty, a deferred release says what it
  waits for, each client's average speed is shown, a search-budget view shows what the last hour's searches were spent
  on, and the Add form shows where a request ranks against the automatic searches.
- **Libraries choose whether existing files are renamed**: never, on request, or automatic. Replacements are
  recorded and shown side by side. Move jobs can be paused, resumed and cancelled.
- **TV**: searches and imports by TheXEM's scene numbers, episode titles in the library's profile languages, air moments on the
  calendar, subtitles that follow the file that replaces an episode, a richer add form (root folder, series type,
  season folders, tags) and a franchise watch task.
- **Per-library music provider order**, label, network and organisation logos, and **people and collections editable
  by hand**; one human is one person across every source.
- **Cache and memory page** (*Settings → Cache*) with one database cache ceiling, a Shrink button, and an optional
  **cache and databases in RAM** (tmpfs); **statistics** per indexer and files below each library's cutoff,
  and health that names a paused indexer or download client.
- **Settings by environment variable**: config folder, port, root folders and API keys; `_FILE` variants for Docker
  secrets. See [environment variables](documentation/environment-variables.md).
- **Deduplication.** Similar videos are found at five levels across all libraries (hash version 2: time-based sample
  windows, black-bar crop, scene cuts), with delete on every file, a frame and 3x3 preview sheet per file, and
  hashing switched off by default.
- **Requests.** Users with request permission file requests an administrator approves (or an *Add* grant that is
  approved at once); tags can be hidden per account and **blocked** library-wide.
- **Target quality.** Libraries aim at a named target with shipped presets, a per-work target, closeness judged
  against it, and a preview of what a change makes upgradable.
- **One verified backup bundle** (`tar.zst` of the four databases and `config.yml`) and a Settings → Database page
  with disk use, a keep rule and maintenance by hand.
- **Held releases** have a size setting (default 200), and torrents can wait for both ratio and time with a hard stop.
- **Documentation** split into beginner, advanced and expert levels, with a getting-started page.
- **Hardening.** The first-run claim race is closed, cross-site writes are refused, a session cookie is `Secure`
  behind a trusted TLS proxy, 6to4 and IPv4-compatible addresses are judged by the IPv4 they carry, symlinks in a
  download are never followed, a torrent whose name is a path never becomes an import path, and a whole download
  folder is never imported as one download.
- **Faster.** Scans read directories ahead of the walk, bulk edit writes only what changed, album decisions are about
  3x faster, release-name parses are remembered, RSS sync skips a feed that answers 304, and the queue asks all
  download clients together.


## 2026.10.001.515 — 2026-10-25

**First public release — early alpha.** Use at your own risk, and please report problems as issues.

- Music, TV, anime and movies in one engine: a successor to Lidarr, Sonarr and Radarr, with any number of libraries, mixed libraries, rating limits and library suggestions
- Hunting within a budget, RSS, upgrade hunting, held releases and free-space gating; SABnzbd and qBittorrent; Newznab/Torznab indexers searched in parallel
- Evidence-based recognition of any folder at any depth, packs and archives, previewed and revertible in one click
- Quality judged from the measured file; ties keep the incumbent; remove-and-block by content hash
- Several metadata providers per library type, in a per-library order, tied together by Wikidata; collections and franchises
- Import from Lidarr, Sonarr, Radarr and Prowlarr (read-only)
- Privacy: no telemetry; the optional public-IP check is now off by default
- Images for linux/amd64 and linux/arm64 on Docker Hub (`makearr/solarr`) and ghcr.io; version scheme `YYYY.MM.RRR.BBB`


## 0.0.22 — 2026-09-04

- Add root folder placement policy and section 11.7's Resolve engine


## 0.0.21 — 2026-09-04

- Add the naming template engine, live-verified against the real library (todo 4.5)


## 0.0.20 — 2026-09-04

- Generalize quality-profile targets beyond bitrate (ADR-40 amendment, todo 4.3)


## 0.0.19 — 2026-09-04

- Wire the measured half of ADR-39 attributes (todo 4.2)


## 0.0.18 — 2026-09-04

- Port Lidarr's release-name parser and quality vocabulary (ADR-18, todo 4.1)
- Bump build to 0.0.17 (internal)
- Add an artist column and configurable, sortable library columns
- Add person list/detail pages and redesign the work detail view
- Bump build to 0.0.16 (internal)
- Add the person read/subscribe API (ADR-32 phase 5)
- Expose credits, genres, description and on-disk status on GET /works/{id}
- Add work.description, sourced from the release group's own Wikidata QID
- Add internal/wikidata and wire real biographies into EnrichJob
- Wire artist credits and genre tags into EnrichJob
- Add artist/credit/genre data and a library.People resolver (ADR-32/33)
- Serve and render cover art (section 21.6, closing the loop)
- Bump build to 0.0.15 (internal)
- Fix internal/parse and SearchIdentifier bugs found by live verification
- Bump build to 0.0.14 (internal)
- Add MusicBrainz text search and wire ADR-24's parse strategy (section 10.2)
- Bump build to 0.0.13 (internal)
- Wire the artwork pipeline into a job handler (section 21.6)
- Bump build to 0.0.12 (internal)
- Add POST /api/v1/score/simulate (F6, section 13.2)
- Validate a quality profile's real shape on write, not just its JSON syntax
- Bump build to 0.0.11 (internal)
- Bump build to 0.0.10 (internal)
- Implement internal/score: ADR-40 quality/language interpreter
- Add internal/artwork resize-and-encode pipeline and Cover Art Archive client
- Bump build to 0.0.9 (internal)
- Add internal/match: section 10.2's weighted-distance identity matcher
- Bump build to 0.0.8 (internal)
- Round-trip quality and language profiles through config export/import
- Declare internal/artwork in the import allowlist
- Declare internal/score and internal/match in the import allowlist
- Bump build to 0.0.7 (internal)
- Add make binfmt: register qemu emulators before a multi-arch image build
- Add quality and language profile schema (ADR-40, section 19.8)
- Bump build to 0.0.6 (internal)
- Add internal/parse: stage 4's ADR-18 music release-name parser + golden corpus
- Bump build to 0.0.5 (internal)
- Collapse the sidebar to icons below 1100px (section 21.8)
- Bump build to 0.0.4 (internal)
- Trim comments to CLAUDE.md's one-line rule before merge
- Add GET /api/v1/works/{id}: read-only work detail page
- Bump build to 0.0.3 (internal)
- Log a startup warning when ADR-30's external tools are missing
- Bump build to 0.0.2 (internal)
- Add GET /api/v1/library/unidentified: F29's manual identification queue
- Add category counts: the sidebar's other, deliberately-deferred half
- Add GET /api/v1/search: ranked, category-filtered title search
- Make the MusicBrainz metadata cache staleness-aware (ADR-29)
- Mark vanished library paths MISSING after a full sweep (section 10.4)
- Add GET /api/v1/categories and wire it into the sidebar and library filter
- Bundle fpcalc and ffprobe in the runtime image (ADR-30)
- Replace the Library placeholder with the real browse page
- Add GET /api/v1/works: the library list endpoint
- Add work_root_folder: a temporary bridge for category filtering
- Wire ADR-29's provider into real edition/work_part enrichment
- Add internal/musicbrainz: ADR-29's provider, both sources
- Add cache/metadata.db: a generic, disposable metadata cache
- Fix env overrides misreading YAML-special leading characters
- Populate a placeholder edition from the release MBID
- Add ADR-15's backup rotation, wired in for real
- Benchmark the scan batch write path at ADR-22 scale
- Add the scan.Consumer: files on disk become identified works
- Persist scan progress and add the first job handler (section 10.4)
- Add internal/scan: the section 10.4 mtime sweep, resumable
- Add the embedded-MBID identifier: identity with no network call (ADR-29)
- Add internal/mediatool: ADR-30's tools, and settle what stage 3 needs
- Recover from a corrupt database at startup, and reconcile after (ADR-15)
- Add backup retention (ADR-15)
- Add discovery_job (ADR-38) and wire it into Invariant 41
- Add the request table's query surface (ADR-14, stage 12)
- Add counter reconciliation logic
- Add internal/metaresolve: field-level provider priority (ADR-8, Inv. 13)
- Add internal/model: ADR-23 extension types and the ADR-24 Identifier chain
- Update benchmarks.md now that FTS5 search exists
- Add FTS5 search over titles and aliases (migration 0010)
- Add event_log and retention_state to jobs.db, decision_rollup to library.db
- Verify the image can supply ADR-30's tools, and qualify the 0.2 milestone
- Give the list's view state to the router, and a DOM to test it with
- Compose the list query instead of writing its filters as no-ops
- Size the page cache per database, and stop pretending the driver is a setting
- Checkpoint the log on purpose instead of by accident
- Measure the ADR-28 targets against a 500k corpus, and settle ADR-5
- Add user management to the interface
- Require an account at setup; open access stays a settings decision
- Unstick the create-account button, and make the login requirement a policy
- Loosen passwords, add themes and icons, and report the egress address
- Fix the API base URL, and build the shell around a dashboard
- Drop history.db: three databases, not four
- Stop interning what is unique per file
- Give parts an identity, and fix the three defects the validation found
- Cut SQL comments to one line, and move the prose to documentation
- Complete library.db: collections, ready queue, artwork, statistics, reputation
- Add people, organisations, tags and subscriptions
- Add candidates, decisions, provenance and recycle bin
- Add desired state, the work-to-file link, the hunt queue and counters
- Plan eleven media types, and add classification and image attributes
- Add the physical layer: objects, paths and interned attributes
- Close five open stage 1 points
- Create library.db and the identity schema
- Tidy doubled references left by the comment pass
- Cut every remaining comment block to one line
- Cut comments back to one line, and add TZ
- Make PUID and PGID authoritative, and say so when they cannot be
- Add F9 config export/import, and stop enforcing host permissions
- Add useLibraryList(), the headless core both library views render
- Add the first-run wizard: claim the install, add a library folder
- Run as `media` 1000:1000, overridable with PUID and PGID
- Make the image work on a first `docker run`, and a fresh config directory
- Add the web interface shell, embedded in the binary
- Implement the generated ServerInterface, and test routing against the spec
- Apply environment overrides on the first run, not just later ones
- Give 405 its own problem type instead of reusing bad-request
- Generate the Go types and TS client from api/openapi.yaml
- Correct the versioning rules and tighten the build context
- Create credential files 0600 instead of world-readable
- Refuse to commit local-only working documents
- Record that database placement is the operator's responsibility
- Add a codebase map to the wiki
- Add the event bus and the SSE stream
- Add cursor pagination and the jobs listing endpoint
- Make multi-arch image builds work
- Add the Dockerfile, healthcheck subcommand and compose example
- Add authentication: passwords, API keys and trusted proxy headers
- Add the HTTP server and a working serve command
- Add job workers, the executor and the dispatch loop
- Add the durable job queue
- Add the authz access context
- Add settings.db, the sqlc stack and the in-memory config cache
- Add secrets: encryption at rest and a type that cannot leak
- Add first-run config.yml creation and key generation
- Add startup directory checks and `config check`
- Add environment overrides for config.yml
- Add config.yml loading and validation
- Add migrations and the pre-migration backup
- Add the priority write queue
- Add the store connection layer
- Add the httpx circuit breaker and Client
- Add the httpx rate limiter
- Add the httpx SSRF guard
- Use '_' when a name contains a path separator
- Add the fsx filesystem seam and path-safety choke point
- Add project skeleton and the clock seam
- Publish to GitHub on every build
- Version by milestone and build, not by commit
- Record deployment and toolchain decisions
- Add Makefile wrapping Go and Node toolchains in Docker
- Support Servarr metadata proxies as preferred, never required
- Adopt generated-code stack: sqlc, OpenAPI spec-first, chi, slog
- Adopt AGPL-3.0-or-later
- Add documentation wiki with coding standards
- Add versioned git hooks and CI workflow
- Rewrite README around functionality
- Ignore local-only CLAUDE.md working context

