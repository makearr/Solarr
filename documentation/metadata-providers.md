# Metadata providers

What each provider carries and which library it can serve. *Built* is in Solarr today; *not built* is a candidate. A
library asks its providers in its own order (the library's Settings, Metadata): the first provider that holds a field
supplies it, each later one only fills what the earlier ones left empty, and a provider left out is not asked. A
library starts with the providers that suit its type and kind, Wikidata first (anime then asks AniList). Settings,
Metadata is one overview: every provider, the libraries it serves, its key, and a switch that takes it out of every
library's order. Music is looked up once for every music library, in the first music library's order, with direct
MusicBrainz after it while MusicBrainz is switched on; any of BrainzMash, Lidarr's service and MusicBrainz may be switched
off, but not the last of them.

**Pace.** Each provider shows what it publishes it can take and the pace Solarr asks it at (`settings.MetadataProviders`,
the one declaration: `PerMinute`, `MaxPerMinute`, `Published`). A person may set their own, a second or a minute, up to the
provider's published limit where it has one (MusicBrainz 1 a second, AcoustID 3, TVmaze 20 per 10 s, AniList 90 a
minute, MDBList 200); it is stored as `metadata.provider_paces` and read by the shared limiter at every request
(`httpx.Config.LivePace`), so it applies without a restart.

**Wikidata steers the others.** Before the other providers are asked, the ids Wikidata's item records for the work
(TheTVDB, TMDB, IMDb, TVmaze, AniList for a series; TMDB, IMDb for a film) fill in what is missing; first in the order,
they also replace the ones they disagree with. The id that found the item (a series' TheTVDB id, a film's TMDB id) is
never changed, and an id two items claim says nothing (`bridgeIDs` in the TV and movie services).

**Wikidata leads what it holds.** Once another provider has identified a work (TMDB for a film, TheTVDB for a series,
MusicBrainz for an album), Wikidata's item is found by that id (`FactsOf` in `internal/metadata/wikidata/facts.go`)
and, first in the order, supplies what it holds before the others: a film's overview (the English Wikipedia article's
lead), original title, year, runtime and original language; a series' overview; an album's genres, which replace
MusicBrainz's voted tags, and its description (already Wikidata's). Measured on a real library: every film sampled is
on Wikidata (56 of 60 with an English article), 56 of 60 series, half the albums, one recording in thirteen. Three
things it does not lead: titles, which name folders and match releases, so they stay the identifying provider's;
film and series genres, which are too fine ("time-travel film", "crime drama television series") and only fill where
no provider names any; songs, which are rarely on Wikidata and stay MusicBrainz's.

Wikidata is read in bulk: one facts query answers 40 works with their ids as well, so the id bridge and the facts
come from one answer, and a process-wide cache (`wikidata.Cached`, six hours) keeps it. A TV or movie refresh sweep
warms the cache with every work it is about to read whose library asks Wikidata (`WarmWikidata`), so each refresh
then asks nothing; an add or a manual refresh reads its one work and keeps it too, a work Wikidata lacks included.

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
| Wikidata | the ids every other provider knows a series by, franchises (part of the series, media franchise, spin-off, follows) | yes | first by default; collections of series |
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
| Wikidata | the ids TMDB and IMDb know a film by | yes | first by default; no key |
| MDBList | ratings of IMDb, TMDB, Trakt, Letterboxd, Rotten Tomatoes, Metacritic in one call | yes | a free key, 1,000 requests a day |
| Trakt | ratings, related films, lists | no | a free key |
| TheTVDB (movies) | English overview, genres, runtime, posters, backgrounds, banners, clear logos, remote ids | yes | Solarr's own key; found by IMDb id, else TMDB's |
| OMDb | IMDb, Rotten Tomatoes and Metacritic ratings, the full English plot | yes | a free key, 1,000 requests a day |
| fanart.tv | clear logos, clear art, disc art, banners, backgrounds (best liked, English first) | yes | a free personal key |
| Trakt, Letterboxd, IMDb lists | lists to import from, not metadata | no | import lists |

## Books and audiobooks

Readarr asked one service of its own (Goodreads, then Bookshelf's rreading-glasses over Hardcover); Audiobookshelf asks
Audible through Audnexus, Open Library, Google Books and iTunes; LazyLibrarian asks Open Library, Google Books and
Hardcover. Solarr asks the keyless ones in each library's order, Wikidata first. How the two types are built:
`documentation/books.md`.

| Provider | Carries | Built | Notes |
|---|---|---|---|
| Wikidata | the ids a book is known by (Open Library work, Goodreads, Google Books, ISBN-13), the English Wikipedia lead, first publication, original language, authors, the series and the book's place in it | yes | first by default; found by Open Library's work id (P648) |
| Open Library | works, their editions' ISBNs, publishers and page counts, authors with photos, subjects, covers | yes | no key; names a book: its work id is the book's id |
| Google Books | descriptions, publishers, page counts, categories, ratings, covers | yes | no key on a shared quota; the owner's free key raises it |
| Audible (Audnexus) | an audiobook's narrators, length, abridgement, series and position, publisher, genres, cover; Audible's catalogue to search | yes | no key; audiobooks only; Audnexus allows 100 a minute |
| Hardcover | works, editions, series, ratings | no | a key; what Bookshelf's service reads |
| iTunes, FantLab | covers, descriptions | no | Audiobookshelf's further sources |

**Wikidata leads what it holds** for books as for films: the overview and the year of first publication, and the
series, which Open Library does not keep. It never names the title: its title is the original language's, kept as an
alias. An audiobook's ISBN is its own reading's, so Wikidata's ISBN fills only a book's.

## Other media types (planned)

Comics: ComicVine, Metron; games: IGDB, ScreenScraper. None is built; a media type is only as viable as its source
(ADR-42).

## How collections are found

| Library | Source | What it gives |
|---|---|---|
| Movies | TMDB collections | the films of a franchise, with the ones the library lacks to add |
| Movies | Wikidata ("part of the series", "media franchise") | a universe or franchise beyond TMDB's collections: Ihaol with Sfozutvaew and Romulus, the Marvel Cinematic Universe with its phases; kept only where it holds a film no single TMDB collection does (`movie.UniversePass`, daily) |
| Series | Wikidata claims (part of the series, media franchise, spin-off, follows) | the series of a franchise, with those the library lacks to add |
| Anime | AniList relations (sequel, prequel, spin-off, side story, parent, alternative) | the series of a franchise the library holds |
| Series | names that say they belong together ("PLUH" and "PLUH: Los Angeles", "Ppamir Vetl Z" and "Ppamir Vetl Super"), characters they share and crossover episodes | grouped by Solarr itself, in a collection of its own |
| Any | made by hand | any works of the library |

Crossover episodes are linked without asking when an episode pair has a title that names the episode (not "Pilot"),
lengths within 5% and a near identical cast; anything less is only suggested, and what a person unlinks is never
linked again.

**Shared characters** (`characterEdges` in `internal/media/tv/related.go`) are a character played by the same person
in two series. Two series are one family when they share three such characters that are regulars (series cast, not one
episode's) in both, or when the newer one is named after a lead (billed in the first ten) or a character in three
episodes of the older one ("Young Brannock" after Brannock Vale). A guest spot is a crossover, not a family; a part named
after the one who plays it is a cameo; hosts, narrators and walk-ons count for nothing; and the two series must share a
genre, a network or a maker. A crossover show joins no family: one whose characters belong to three families or more,
or to two when most of its characters are other series'.

Every pass reads Wikidata for the whole library in a few bulk queries (`TiesOf`, `FranchiseMembers` in
`internal/metadata/wikidata/ties.go`) and works the families out again:

- A franchise item ("part of the series", "media franchise") is a collection named by Wikidata, holding the series
  Wikidata lists in it; a franchise that is part (P361) of another the library also has a series of joins it.
- An item that is itself a series is a programme strand, not a franchise: the series in it relate to the strand only.
- Spin-off and follows/followed-by claims are evidence between series, and name nothing.
- Episodes listed under two series count once each; one shared broadcast needs a genre in common as well.
- Solarr's own collections, and Wikidata's, are rebuilt from the evidence of the run and go when it does; when
  Wikidata cannot be read, its collections stay as they are.
- A family is called by the name all its series share ("Vulcano Park" for "Vulcano Park: …" series), else by its
  provider's, else by its oldest series.
- What a collection lacks, and the Y of "X / Y", are the series Wikidata ties to its members that the library lacks.

**TMDB ids** are stored under one key per kind, because TMDB numbers each kind on its own: `tmdb` for series, people
and networks, `tmdb:movie` for films, `tmdb:company` for studios, `tmdb:collection` for collections
(`internal/metadata/tmdb/keys.go`). A lookup by provider id also names the media type it wants.
