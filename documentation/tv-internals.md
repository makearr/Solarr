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

## Providers (`internal/metadata/skyhook`, `tvmaze`)

Skyhook is Sonarr's TheTVDB proxy (series, episodes in aired order with absolute numbers, cast, artwork); TVmaze is keyless
and the best source of air times (`airstamp`) and the network's timezone. Each gets its own rate bucket when wired
(ADR-13): TVmaze allows 20 calls per 10 seconds.

`internal/media/tv` maps each into a `Series` and `Merge`s them: the first source wins field by field, a later one fills
gaps, aliases are unioned, and an episode takes the most trustworthy air time (`high` TVmaze airstamp, `medium` TVDB
UTC, `low` date only). TVmaze alone is enough to list episodes.

## Storage (`library.Series.Upsert`)

A series is a `work` (media type `tv`), an episode a `work_part` with `media_ref` `s03e10` and `sort_key` `s0003e0010`.
The writer finds the work by any provider id it carries (never stealing an id another work owns), keeps a manual title,
replaces one provider's aliases, writes network, genres, rating and dates, and upserts parts in 200-row transactions. **A
refresh never changes a part's monitoring and never removes a part.** Part availability (`available_at`, confidence,
basis), part-level collection members and per-work release filters are the generic columns and tables of migration 0059.

## Naming (`internal/media/tv/naming.go`)

Sonarr's six formats (standard, daily, anime, series folder, season folder, specials folder), its token spellings and
the six multi-episode styles. The shipped default reproduces the user's library: `Season 3` unpadded, `Specials`,
`S03E10-E11` and several episode titles joined with `+`. The style is stored as one more entry of the profile's formats
(`multi_episode_style`) and is stripped before template validation. Plex and Jellyfin presets write their id folders with
the engine's escapes: `{{` and `}}` for braces, `[[` and `]]` for brackets.
