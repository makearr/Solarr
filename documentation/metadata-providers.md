# Metadata providers

What each provider carries and which library it can serve. *Built* is in Solarr today; *not built* is a candidate. A
library asks its providers in order (Settings, Metadata for the instance's order; a library's Settings, Metadata for its
own): the first provider that holds a field supplies it, each later one only fills what the earlier ones left empty,
and a provider left out is not asked. A new library starts with the providers that suit its type (anime asks AniList first).

## What the upstream projects use

| Project | Source of metadata | Notes |
|---|---|---|
| Radarr | its own service (api.radarr.video) over TMDB, with IMDb, Rotten Tomatoes, Metacritic and Trakt ratings | one source, no per-library choice; lists come from TMDB, Trakt, IMDb, Plex |
| Sonarr | SkyHook, its own service over TheTVDB | one source; TVmaze and TMDB are only used for lists |
| Lidarr | its own service (a MusicBrainz mirror with Wikipedia, Fanart and TheAudioDB data folded in) | one source; Cover Art Archive for covers |
| Jellyfin | built in: TMDB, OMDb, MusicBrainz, TheAudioDB, local nfo; plugins: TheTVDB, fanart.tv, AniDB, AniList, AniSearch, TVmaze | **a downloader list per library, in order**; Jellyfin documents TMDB, OMDb and local nfo as built in and TheTVDB, fanart.tv and AniDB as catalogue plugins |

Jellyfin's per-library ordered downloaders are the model Solarr follows. What each upstream project uses beyond what its
documentation states is from memory and should be checked before relying on it.

## Music

| Provider | Carries | Built | Notes |
|---|---|---|---|
| MusicBrainz | artists, release groups, releases, tracklists with durations, credits, genres and tags, URL relations (Wikidata, streaming) | yes | 1 request a second |
| BrainzMash | the same, 10 requests a second, a mirror Lidarr's clients use | yes | needs a `Lidarr/` user agent |
| Lidarr's metadata service | the same with images and links folded in | yes | a proxy, never required |
| Cover Art Archive | album covers (front, back, booklet) by release or release group | yes | covers only |
| TheAudioDB | artist biographies, thumbs, fanart, logos, banners; album art, descriptions, mood, style | yes | free key |
| Wikidata, Wikipedia | the id bridge between providers, biographies, photos (Commons) | yes | people |
| AcoustID | an audio fingerprint to a recording id | yes | an identifier, not a source |
| fanart.tv | artist backgrounds, logos, banners, album covers, disc art | no | a free key; best for images |
| Discogs | credits, labels, catalogue numbers, pressings | no | needs a token |
| Last.fm | tags, biographies, similar artists, listening counts | no | |

## TV

| Provider | Carries | Built | Notes |
|---|---|---|---|
| SkyHook | series, seasons, episodes with air dates, images, the ids of TheTVDB, TMDB, IMDb, TVmaze, AniList | yes | no key; sets identity and numbering |
| TheTVDB v4 | the same, other orderings (DVD, absolute), artwork kinds, cast, companies, remote ids | yes | a key |
| TMDB | overview, cast and crew, guest stars per episode, networks, keywords, certification, episode groups, artwork, external ids (Wikidata, IMDb, TheTVDB) | yes | a free key |
| TVmaze | air times with time zones, networks, cast, images, runtimes | yes | no key; alone knows the zone |
| AniList | titles in every language, synonyms, cours, format, status, genres, covers, banners, **relations** (sequel, prequel, spin-off) | yes | anime |
| Wikidata | franchises (part of the series, media franchise, spin-off, follows), ids of TheTVDB and TMDB | yes | collections of series |
| AniDB, Kitsu, MyAnimeList | anime titles, episodes, relations, ratings | no | AniDB bans heavy clients |
| Trakt | lists, ratings, calendars, related shows | no | a key; popular lists |
| IMDb datasets | ratings, episode lists, crew | no | files, not an API |
| fanart.tv | clear logos, banners, HD art, season posters | no | a free key |
| OMDb | ratings (IMDb, Rotten Tomatoes), English plots | no | a key |

## Movies

| Provider | Carries | Built | Notes |
|---|---|---|---|
| TMDB | overview, cast and crew, releases and certification by country, studios, keywords, translations, **collections**, artwork | yes | a free key |
| Radarr's metadata service | TMDB's films with IMDb, Rotten Tomatoes, Metacritic and Trakt ratings, release dates, collections | yes | no key; the fallback behind TMDB |
| TheTVDB (movies) | films, artwork, remote ids | no | |
| OMDb | ratings, English plots | no | |
| fanart.tv | clear logos, banners, disc art | no | |
| Trakt, Letterboxd, IMDb lists | lists to import from, not metadata | no | import lists |

## Other media types (planned)

Books and audiobooks: Open Library, Hardcover, Google Books, Audible metadata; comics: ComicVine, Metron; games: IGDB,
ScreenScraper. None is built; a media type is only as viable as its source (ADR-42).

## How collections are found

| Library | Source | What it gives |
|---|---|---|
| Movies | TMDB collections | the films of a franchise, with the ones the library lacks to add |
| Series | Wikidata claims (part of the series, media franchise, spin-off, follows) | the series of a franchise, with those the library lacks to add |
| Anime | AniList relations (sequel, prequel, spin-off, side story, parent, alternative) | the series of a franchise the library holds |
| Series | names that say they belong together ("PLUH" and "PLUH: Los Angeles", "Ppamir Vetl Z" and "Ppamir Vetl Super") and crossover episodes | grouped by Solarr itself, in a collection of its own |
| Any | made by hand | any works of the library |

Crossover episodes are linked without asking when an episode pair has a title that names the episode (not "Pilot"),
lengths within 5% and a near identical cast; anything less is only suggested, and what a person unlinks is never
linked again.
