# TV (stage 8): what exists and how it fits

TV is built after music (`private/next.md` section 6 holds the plan and its open items). This page records what the
code does, not the plan.

## Parsing a release or file name (`internal/identify/parsetv`)

Sonarr's pattern table, run unchanged on `dlclark/regexp2` because the patterns need lookaround, backreferences and
duplicate group names that Go's `regexp` rejects (ADR-18, `ATTRIBUTION.md`). `Parse(name, now)` returns an `Info`:
series title and aliases (`A / B`, `A (B)`, `A AKA B`), season, episodes, absolute numbers, air date, full-season,
partial-season, multi-season, mini-series, special and split-episode flags, release group and hash, and `Claims`
(resolution, source, remux, codec, HDR, audio, streaming service, revision, languages). `ParsePath` combines the folder
and file name as Sonarr does. Time is an argument: an air date in the future is refused.

Claims keep resolution and source apart (ADR-40); Sonarr's single `Quality` enum is not used. A dimension the name does
not state is zero. The golden corpus (`testdata/*.jsonl`) is Sonarr's own test fixtures extracted mechanically.

## Providers (`internal/metadata/{skyhook,tvmaze,tmdb,tvdb}`)

Skyhook is Sonarr's TheTVDB proxy (series, episodes in aired order with absolute numbers, cast, artwork); TVmaze is keyless
and the best source of air times (`airstamp`) and the network's timezone. **TMDB** needs the user's own key (Settings →
Metadata; a v4 read token or a v3 key, sent as bearer or parameter) and adds the overview, genres, certification, artwork,
networks with logos, keywords, alternative titles and the credits: aggregate cast, key crew jobs, and per season the guest
stars and each episode's director and writers. **TheTVDB v4** has a client (login reused, every ordering by season type)
and Solarr's own project key as the default; nothing reads it yet. **AniList** (GraphQL, no key) is asked for each cour Skyhook
lists an AniList id for (up to five) and adds the romaji, English and native titles and the synonyms as aliases, plus the
banner: release groups name anime by those, so they are what title matching needs. Each provider has its own rate bucket (ADR-13).

`internal/media/tv` maps each into a `Series` and `Merge`s them. **Skyhook sets the identity and the numbering basis** (title,
year, dates, status, ids, the episode list), so a library named after TheTVDB keeps its names; `PreferRicher` then lets TMDB
win the overview, genres, certification and artwork order, and TVmaze adds air times (`high` airstamp, `medium` TVDB UTC,
`low` date only) and aliases. TMDB carries **no episode list**: its default order is not the basis, so an episode-scoped
credit is kept only when its episode aired on the same date under the basis (`guardEpisodeCredits`). TVmaze alone is enough
to list episodes; without a TMDB key everything works on the other two. **Only when no other source lists episodes** (a show TheTVDB
lacks, typically new or announced) TMDB's own list becomes the basis, stored as `numbering_basis` `tmdb:default`. Search asks Skyhook,
and TMDB too when keyed, dropping a TMDB hit already found at Skyhook (by TMDB id, else by title without "(US)" and year); a
TMDB-only hit is added by its TMDB id (`provider: tmdb`), and TheTVDB's id is looked up from TMDB's external ids when it has one.

## Storage (`library.Series.Upsert`)

A series is a `work` (media type `tv`), an episode a `work_part` with `media_ref` `s03e10` and `sort_key` `s0003e0010`.
The writer finds the work by any provider id it carries (never stealing an id another work owns), keeps a manual title,
replaces one provider's aliases, writes network, genres, rating and dates, and upserts parts in 200-row transactions. **A
refresh never changes a part's monitoring and never removes a part.** Part availability (`available_at`, confidence,
basis), part-level collection members and per-work release filters are the generic columns and tables of migration 0059.

Credits are `person` rows keyed by `tmdb:<id>` and `credit` rows on the work (cast, creator, crew roles) or on the episode
(`part_id`: guest star, director, writer); the person's photo URL lives in `fields_json`, the biography fills in when the
person page asks TMDB what the person is known for (`tv.Profiling`, `GET /people/{id}/known-for`). Networks are
`organisation` rows (`kind` network, logo in `fields_json`) linked by `work_organisation`; keywords are
`work_classification` rows. A refresh adds and updates credits and never removes one. The Cast and Networks levels are
the one people and organisations list of every media type (`GET /people`, `GET /organisations`, `person_library` and
`organisation_library` in `database-layout.md`), and a person or network has one page (`/people/{id}`, `/organisations/{id}`).

## Monitoring a held series (`Service.ApplyMonitor`, Season pass)

`PUT /series/{id}/monitoring` re-applies one of Sonarr's nine monitor choices to what is already held: every episode becomes
monitored or not as the choice says ("existing" wants the episodes with a file, "missing" those without) and each season item
follows (`Desired.ApplyPartMonitoring`, one transaction per season). A season item is monitored exactly when one of its
episodes is, also when one episode is toggled by hand, so wanting a single episode of an unmonitored season reaches the
planner. `PUT /series/{id}/quality` gives every season of a series its own quality profile (the album precedent,
`desired_profile_override`). The Season pass screen (a grid of series and season chips over these) is reachable at `/library/<id>/seasonpass` but no longer in the navigation (2026-10-03, user).

## Deleting, series type, pictures

`DELETE /series/{id}` removes a series with its episodes, credits and desired items; `delete_files` sends its files to the recycle
bin (never deleted directly) and `exclude` writes every id the series carries to `work_exclusion` (generic over media types), so the scan (files left
alone, not queued), a follow and an import list leave it out; adding it by hand lifts the exclusion. **Series type** (`series_type`
field) is detected on every add and refresh (anime: Japanese and listed by AniList, AniDB or MyAnimeList; daily: a talk, news, soap
or game show) and the user's own choice (`__manual__`) wins; naming reads it, and an anime season is also searched by absolute
number ("Title 062", up to five missing episodes) with absolute-only releases matched to their episode. Provider pictures are
served by `GET /images/remote` from `internal/platform/remoteimage`: fetched once from an allow-listed https image host, resized
to the three cover sizes and stored in the flat `cache/images` directory (so the budget evicts them), and the browser never
contacts a provider's CDN.

## One person across libraries (`library.People.LinkWikidata`, `tv.BridgeJob`)

The Wikidata QID is a `person_external_id` (provider `wikidata`, unique): linking a QID another person already holds merges the two
with the music-artist person surviving (`mergeArtist`, the loser's name becomes an alias; a follow held by both keeps the
survivor's). The QID comes from MusicBrainz (stored by artist refresh and backfill) and from TMDB's `external_ids.wikidata_id`
(stored when a person page opens and by the daily `people.bridge` sweep, which only asks about TMDB people whose `sort_name`
equals a music artist's, 60 a run; `wikidata:none` records an answer of "no item"). The one person page then shows their
works in every library the viewer may read. Merging on name alone is deliberately not done. The sweep also asks Wikidata (`MusicBrainzArtists`, property P434) which music artist an item is, so an actor is merged into the artist the library already holds even before that artist's own refresh recorded an item; measured on the real library: 42 people merged at once (a singer in a series' guest cast is one person in both libraries).

## Following people and networks (`internal/media/tv/follow.go`)

Following is one model for every media type (`internal/service/follow`): a `subscription` row per subject, user and
library (migration 0065), with roles, the media type's options in `filters_json` and a back-catalogue flag. The sweep
`follow.expand` (task "Add the works of followed people") asks each follow's media type for candidates
(`media.Following`), skips what the library holds or excluded (`work_exclusion`), and adds up to the type's limit; the
preview (`POST /people/{id}/follow/preview`, `/organisations/{id}/follow/preview`) runs the same candidates. TV's
candidates come from TMDB: a person's combined credits (tv only, the followed roles, first aired since the follow was
made less 30 days, or a regular role with 3+ episodes for a back-catalogue follow) or `/discover/tv` for a network. Each
show is added with the follow's `monitor` option, **at most 5 per follow per run** (25 for a back catalogue). A show
whose TMDB id has no TheTVDB id is skipped (no episode list). Making or changing a follow enqueues its own run.

A scan's monitoring of series it finds is the library's `tv_monitor` (`category.tv_monitor`): `existing` (default; only the episodes
on disk), `all` (Sonarr's adoption: hunts every gap) or `none` (nothing, also not the episodes found).

## Naming (`internal/media/tv/naming.go`)

Sonarr's six formats (standard, daily, anime, series folder, season folder, specials folder), its token spellings and
the six multi-episode styles. The shipped default reproduces the user's library: `Season 3` unpadded, `Specials`,
`S03E10-E11` and several episode titles joined with `+`. The style is stored as one more entry of the profile's formats
(`multi_episode_style`) and is stripped before template validation. Plex and Jellyfin presets write their id folders with
the engine's escapes: `{{` and `}}` for braces, `[[` and `]]` for brackets.

## Identifying a folder (`Resolver`, `RetryJob`)

A folder is named in this order: `{tvdb-N}` in the name, a title or alias already in the library, then the providers' search. `pick`
accepts the one candidate whose title matches (ignoring TheTVDB's "(2014)" or "(US)" qualifier and TMDB's original title, which
travels as `Candidate.AltTitles`); when several match, or none does and the folder has at least 8 numbered files, `byEpisodes` fetches the
top candidates from Skyhook and takes the one that has at least 80 % of the files' season/episode (or absolute) numbers and leads the next
by 15 points. Anything less sure is queued for a person. A scan only hands over new or changed files, so `RetryJob` (task `tv_retry`, daily,
and the Review page's button) feeds the waiting files through `Consumer.Observe` again. Measured on the user's 1,062-series library: 5,553 of
55,970 series files were waiting before; see inflight.md for what remained.

Speed: the season directories of one series are read side by side by the scan (eight directories at once), so `FolderLocks` makes them ask the
providers once between them, and TVmaze's cast is skipped when a TMDB key is stored (its bucket is the slowest). Measured 2026-10-03 on 60
real series (2,167 files, probing included): 300 s before, 130 s after.

## Measured video facts and quality (`probe`, `judge.WithTarget`, `quality.Judge`)

The probe (`internal/scanning/probe`) reads a video file's resolution, codec (`hevc` becomes `h265`), HDR (`dolby-vision`, `hdr10plus`, `hdr10`,
`hlg`; empty is sdr), frame rate, scan type, audio codec and channel layout, and subtitle languages: embedded tracks plus sidecar files
named like the video (`probe.sidecarSubtitleLanguages`; a sidecar with no language in its name is `und`). They are interned in
`media_attributes` as the measured half (ADR-39). Files measured before this are read again (`ListStaleVideoProbes`, keyed on the stored probe
JSON lacking `SubtitleLanguages`), a batch at a time (the tool runs 16 ffprobe processes side by side).
A TV library's target is resolution, video codec, HDR, audio codec and file type (`settings.QualityTarget`, migration 0034); `judge.WithTarget` makes
the named resolution/codec/HDR the best tier of their dimension and puts them first in the comparison, inside the profile's own gates, and
`quality.MeetsTarget` judges a file (every named part equal). `SeriesReader.HeldEpisodes` + `quality.Judge` feed Wanted > Cutoff unmet.

## Upgrades

A monitored season whose episodes are all present is judged by `tv.cutoff_check` (nightly and after every import of that series) against the library's target or cutoff, using what the probe measured: any monitored episode file below it makes the season UPGRADABLE, the state the planner already hunts as an upgrade (24 h minimum file age, three imports a week by default, from the profile's `upgrade` block). A season that still has gaps stays PARTIAL and hunts its gaps first.

The search of an UPGRADABLE season wants only the episodes below the cutoff and rejects a release that does not beat the file it replaces on the quality tiers (a tie keeps the owned file). Source, release group and repack are left out of that comparison: a release name states them and a measured file does not, so they would make every release look better. The importer places an upgrade next to the owned file (suffix ` [upgrade]` when both are named alike) and the cutoff check, once both files are measured, keeps the better one and moves the other to the recycle bin (reason `replaced`, or `rejected_worse` for a download that was no better).

The shipped TV profile **Any** also takes a small additive penalty for a release under 0.4 Mbps or over 25 Mbps of video, so a starved or bloated file loses a tie.

