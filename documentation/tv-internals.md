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
person page is first opened. Networks are `organisation` rows (`kind` network, logo in `fields_json`) linked by
`work_organisation`; keywords are `work_classification` rows. A refresh adds and updates credits and never removes one.
The People and Networks levels read these through `library.SeriesReader` (`/series/people`, `/series/networks`).

## Monitoring a held series (`Service.ApplyMonitor`, Season pass)

`PUT /series/{id}/monitoring` re-applies one of Sonarr's nine monitor choices to what is already held: every episode becomes
monitored or not as the choice says ("existing" wants the episodes with a file, "missing" those without) and each season item
follows (`Desired.ApplyPartMonitoring`, one transaction per season). A season item is monitored exactly when one of its
episodes is, also when one episode is toggled by hand, so wanting a single episode of an unmonitored season reaches the
planner. `PUT /series/{id}/quality` gives every season of a series its own quality profile (the album precedent,
`desired_profile_override`). The Season pass level is a grid of series and season chips over these.

## Deleting, series type, pictures

`DELETE /series/{id}` removes a series with its episodes, credits and desired items; `delete_files` sends its files to the recycle
bin (never deleted directly) and `exclude` writes every id the series carries to `series_exclusion`, so the scan (files left
alone, not queued), a follow and an import list leave it out; adding it by hand lifts the exclusion. **Series type** (`series_type`
field) is detected on every add and refresh (anime: Japanese and listed by AniList, AniDB or MyAnimeList; daily: a talk, news, soap
or game show) and the user's own choice (`__manual__`) wins; naming reads it, and an anime season is also searched by absolute
number ("Title 062", up to five missing episodes) with absolute-only releases matched to their episode. Provider pictures are
served by `GET /images/remote` from `internal/platform/remoteimage`: fetched once from an allow-listed https image host, resized
to the three cover sizes and stored in the flat `cache/images` directory (so the budget evicts them), and the browser never
contacts a provider's CDN.

## Following people and networks (`internal/media/tv/follow_job.go`)

A person (by role) or a network can be followed from its page: a `subscription` row (media type `tv`, roles, the library it adds
to, `filters_json` `{"monitor": ...}`, back-catalogue flag). The registered task "Subscription expand" also enqueues
`tv.follow_expand`, which asks TMDB per follow: a person's combined credits (tv only, the followed roles, first aired since the
follow was made less 30 days, or a regular role with 3+ episodes for a back-catalogue follow) or `/discover/tv` for a network. Each
show not yet held is added with the follow's monitor choice, **at most 5 per follow per run** (25 for a back catalogue). Additive
only, and a show whose TMDB id has no TheTVDB id is skipped (no episode list). Making or changing a follow enqueues its own run.

A scan's monitoring of series it finds is the library's `tv_monitor` (`category.tv_monitor`): `existing` (default; only the episodes
on disk), `all` (Sonarr's adoption: hunts every gap) or `none` (nothing, also not the episodes found).

## Naming (`internal/media/tv/naming.go`)

Sonarr's six formats (standard, daily, anime, series folder, season folder, specials folder), its token spellings and
the six multi-episode styles. The shipped default reproduces the user's library: `Season 3` unpadded, `Specials`,
`S03E10-E11` and several episode titles joined with `+`. The style is stored as one more entry of the profile's formats
(`multi_episode_style`) and is stripped before template validation. Plex and Jellyfin presets write their id folders with
the engine's escapes: `{{` and `}}` for braces, `[[` and `]]` for brackets.
