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

## Monitoring a held series (`Service.ApplyMonitor`)

`POST /works/{id}/monitor` with an `option` (TV's `media.Monitoring`; the same endpoint takes a season as `group`
and episodes as `parts`, for every media type) re-applies one of Sonarr's nine monitor choices to what is already held: every episode becomes
monitored or not as the choice says ("existing" wants the episodes with a file, "missing" those without) and each season item
follows (`Desired.ApplyPartMonitoring`, one transaction per season). A season item is monitored exactly when one of its
episodes is, also when one episode is toggled by hand, so wanting a single episode of an unmonitored season reaches the
planner. `PUT /works/{id}/edit` gives every season of a series its own quality profile (the album precedent,
`desired_profile_override`) and changes monitoring only when the person changed it, so saving tags keeps a series'
season-by-season choice; `POST /works/bulk/edit` applies a monitor option, the profile, tags and the series type to a
selection, which replaced the Season pass screen.

## When an episode counts as missing (`library.PartOut`, `Desired.RecountComingOut`)

A season owes its monitored episodes that are **out**, not all of them, so a season still airing is Complete
between episodes instead of Partial until its finale. The rule is the core's, on `work_part`'s availability columns,
and holds for any media type whose parts come out one at a time:

| What the part has | Out when |
|---|---|
| A time (`available_at`) | that time plus the part's grace (`available_grace`): by confidence, high 2 h, medium 12 h, low or unknown 36 h by default |
| A day only (`date`) | the low grace (36 h by default) after the day begins (UTC): the whole day in an unknown zone |
| Neither, and announced (`available_basis = unannounced`, what TV writes for an episode without a date) | never, until it gets one |
| Neither (a track) | always |

The three graces are advanced settings (Settings -> Metadata -> Air times, `availability.grace_*_hours`, 0-336 h). The TV
write stamps them on each part; a part stamped before keeps its value until **Count what has come out** (every 15
minutes, and at once when the settings change) re-stamps every part whose grace differs (`Series.StampGrace`, 200 a batch),
then re-counts the season items of works with a part whose time or day is within the longest grace plus 36 h or still
ahead, writing only those whose count changed; the planner sees them as dirty. A season owing nothing yet carries its next
episode's time as `desired_item.available_from` (basis `parts`): its header reads "Not out yet", lists "Not released yet".

A series' **Air time zone** (Edit, an editable facet that refreshes the series) is the person's word over the providers':
every dated episode then airs at the series' local air time in that zone (confidence medium, basis "series air time"). The season search asks for an episode by the same rule,
grace included. Wanted > Missing lists an episode from its time or day without the grace. An episode stored before
this rule with neither time nor date counts as owed until the series is next refreshed.

## Deleting, series type, pictures

`POST /works/{id}/remove` removes a work of any type; `delete_files` sends its files to the recycle bin (never deleted directly)
and `exclude` writes every id the work carries to `work_exclusion`, so a follow and an import list leave it out. TV declares
`ScanHonoursExclusions` (the scan leaves an excluded series' files alone, not queued), so an excluded series goes with its
episodes, credits and desired items although its files stay; an album with files stays. Adding it by hand lifts the exclusion.
`POST /works/{id}/refresh` reads a work again through its type's `media.Refreshing` (a series at once, an album in the
background). `GET /works/{id}/artwork/options` and `PUT /works/{id}/artwork` choose a work's picture through its type's
`media.Picturing`: a series lists the posters its sources gave and keeps the pick first in its `__manual__` `images` field. **Series type** (`series_type`
field) is detected on every add and refresh (anime: Japanese and listed by AniList, AniDB or MyAnimeList; daily: a talk, news, soap
or game show) and the user's own choice (`__manual__`, set as the editable `series_type` facet through `PUT /works/{id}/edit`'s `fields`) wins; naming reads it, and an anime season is also searched by absolute
number ("Title 062", up to five missing episodes) with absolute-only releases matched to their episode. Provider pictures are
served by `GET /images/remote` from `internal/platform/remoteimage`: fetched once from an allow-listed https image host, resized
to the three cover sizes and stored in the flat `cache/images` directory (so the budget evicts them), and the browser never
contacts a provider's CDN.

## One person across libraries (`library.People.LinkWikidata`, `tv.BridgeJob`)

The Wikidata QID is a `person_external_id` (provider `wikidata`, unique): linking a QID another person already holds merges the two
with the music-artist person surviving (`mergeArtist`, the loser's name becomes an alias; a follow held by both keeps the
survivor's). The QID comes from MusicBrainz (stored by artist refresh and backfill) and from TMDB's `external_ids.wikidata_id`
(stored when a person page opens and by the daily `people.bridge` sweep, which only asks about TMDB people whose `sort_name`
equals a music artist's, 300 a run; `wikidata:none` records an answer of "no item"). The one person page then shows their
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


## Library kinds (T3)

A media type may declare **kinds** of library (`media.Descriptor.Kinds`); TV declares series, anime, documentary,
reality and kids (`internal/media/tv/kinds.go`). Adding a library takes one (`POST /categories` `kind`, the type's first
when omitted, stored as `category.kind`, settings migration 0041) and the API applies its defaults once: the named
quality profiles in order (one not found is skipped) and a language profile found by name or created. Changing the
kind later applies nothing. TV gives anime its own meaning: `Service.Add` makes a series it creates in an anime library
anime (a manual field) when the providers say standard, and every query of an anime-type series also asks Newznab
5070 (`judge.Query.MoreCategories`), as Sonarr's anime categories do.

## Numbering by another ordering (T4c)

A series can be numbered by another of TheTVDB's orderings (DVD, absolute, alternate, regional, altdvd), as SickGear
switches a show's list and Medusa offers DVD order: the series page's **Numbering** (administrators; shown only when
TheTVDB lists another ordering) previews every episode that moves and applies it (`GET/POST /works/{id}/numbering`,
the `media.Renumbering` capability). TheTVDB's episode ids are the same in every ordering, so each part is matched by its
`tvdb:episode` id: it moves to its place in the new list keeping its id, files, credits and history
(`library.Series.Renumber`, through temporary refs so two parts can trade places in one transaction). A part the list
lacks whose place it gives to another is set aside under `unplaced:<id>`, its files kept, in no season. The choice is the
manual field `numbering_basis` (`tvdb:dvd`, ...; empty is aired) and every later refresh numbers by it
(`Service.keepOrdering`), moving parts again if TheTVDB's list itself shifted. Files keep their names until renamed
(the series' Rename preview). Not built: numbering by another *source* (TMDB, TVmaze), whose episodes share no id
with TheTVDB's.

## Crossovers (T23)

Two kinds, on generic tables. An **event across series** is a `collection` of kind `crossover` whose members are parts
(`collection_member.part_id`, migration 0059) in watch order; a part shows "Event, 2 of 4" and links to the event's page,
which can monitor every episode at once (`library.Crossovers`, `/crossovers`). **One broadcast listed under two series**
is a `relationship` (`part` -> `part`, kind `same_content`): a file covering either episode covers the other too, on
linking and whenever files are attached (`AttachFiles` -> `coverLinkedTx`), so the episode counts as on disk in both. The
library's **Crossovers** level (a `collection` level entity) lists the events and suggests pairs of episodes of two
series with one title on one day; nothing is linked without a person. Not built yet: placing a hardlink of the file in
the second series' folder (a media server still sees it under one series only), suggestions from file hashes or TMDB
episode notes, and a release named after one series searched for the other's want.
