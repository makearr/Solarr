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
and Solarr's own project key as the default; nothing reads it yet. Each provider has its own rate bucket (ADR-13).

`internal/media/tv` maps each into a `Series` and `Merge`s them. **Skyhook sets the identity and the numbering basis** (title,
year, dates, status, ids, the episode list), so a library named after TheTVDB keeps its names; `PreferRicher` then lets TMDB
win the overview, genres, certification and artwork order, and TVmaze adds air times (`high` airstamp, `medium` TVDB UTC,
`low` date only) and aliases. TMDB carries **no episode list**: its default order is not the basis, so an episode-scoped
credit is kept only when its episode aired on the same date under the basis (`guardEpisodeCredits`). TVmaze alone is enough
to list episodes; without a TMDB key everything works on the other two.

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

## Naming (`internal/media/tv/naming.go`)

Sonarr's six formats (standard, daily, anime, series folder, season folder, specials folder), its token spellings and
the six multi-episode styles. The shipped default reproduces the user's library: `Season 3` unpadded, `Specials`,
`S03E10-E11` and several episode titles joined with `+`. The style is stored as one more entry of the profile's formats
(`multi_episode_style`) and is stripped before template validation. Plex and Jellyfin presets write their id folders with
the engine's escapes: `{{` and `}}` for braces, `[[` and `]]` for brackets.
