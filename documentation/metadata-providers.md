# Metadata providers

> **Level: 🟡 Advanced.** What each provider carries and which library it serves.

What each provider carries and which library it can serve. *Built* is in Solarr today; *not built* is a candidate. A
library asks its providers in its own order (the library's Settings, Metadata): the first provider that holds a field
supplies it, each later one only fills what the earlier ones left empty, and a provider left out is not asked. A
library starts with the providers that suit its type and kind, Wikidata first (anime then asks AniList). Settings,
Metadata is one overview: every provider, the libraries it serves, its key, and a switch that takes it out of every
library's order. Music too is looked up in each library's own order, with direct MusicBrainz after it while
MusicBrainz is switched on; any of BrainzMash, Lidarr's service and MusicBrainz may be switched off, but not the last of
them.

**Which music library a lookup is for.** A lookup carries its library on its context (`musicbrainz.ForLibrary`); the
provider reads that library's order per lookup (`Snapshot.MusicSourceOrder`). A scan, an import, a file placed by hand,
the Add search and a follow carry the library they run in; the discography coverage job asks per library. Enrichment,
refresh and the label backfill work per album or artist, so they ask `music.Libraries`: an album's first music library
that wants it, else the first holding its files; an artist's first music library holding their works
(`person_library`). An album held by two music libraries is one work with one set of metadata, so it follows the first.
A lookup with no library (an actor's albums on a person page, the artist picture picker, an import deciding its library
per unit) takes the first music library's order, as every lookup did before.

**Which source fills which album field.** Direct MusicBrainz alone fills an edition's disambiguation, packaging, language and script, a disc's title, a track's credited artists and ISRCs, and a release group's folksonomy tags and aliases; the Servarr proxy's shape carries none of them, so those fields stay empty from it and the library keeps what an earlier direct answer stored.

**The cache is keyed by order.** Every MusicBrainz cache entry's kind carries the order it was asked in, the source names
in order (`musicbrainz:release-group@brainzmash>musicbrainz-proxy>musicbrainz`), so an answer one order got, a fallback
included, is never served to an order that ranks the sources otherwise, and reordering a library asks again. An entry
from before orders were tagged (the bare kind) is served only when the source that gave it is the first able to answer
in the asking order: that answer is the one the order asks for, whatever ranks after it. Those entries age out under
`refresh_after` and the cache budget like any other.

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
| Radarr | its own service (api.radarr.video) over TMDB, with IMDb, Rotten Tomatoes and Metacritic ratings | one source, no per-library choice; lists come from TMDB, IMDb, Plex |
| Sonarr | SkyHook, its own service over TheTVDB | one source; TVmaze and TMDB are only used for lists |
| Lidarr | its own service (a MusicBrainz mirror with Wikipedia, Fanart and TheAudioDB data folded in) | one source; Cover Art Archive for covers |
| Jellyfin | built in: TMDB, OMDb, MusicBrainz, TheAudioDB, local nfo; plugins: TheTVDB, fanart.tv, AniDB, AniList, AniSearch, TVmaze | **a downloader list per library, in order**; Jellyfin documents TMDB, OMDb and local nfo as built in and TheTVDB, fanart.tv and AniDB as catalogue plugins |

Jellyfin's per-library ordered downloaders are the model Solarr follows. What each upstream project uses beyond what its
documentation states is from memory and should be checked before relying on it.

## Your own media servers

A media server connected under Connections, Media servers with *Use as a metadata source* on, and mapped to a library, is a
provider of that library, `local:<kind>`. It is **provisional**: asked first in time (so a work has something at once), then
every other provider in the library's order; where both hold a field the other provider wins, and the server's value stays only
where no one else has one. Its ids only fill gaps, never replace an id from a file, a person or another provider. A library's
Metadata page marks it "your media server" and says so; a type's default order never holds one, only a library's own does.
A connection a library's order still names is not deleted (409) unless another of its kind answers for that library.

| Server | Libraries | Asked by | Notes |
|---|---|---|---|
| Jellyfin | TV, movies, music | provider id from a folder or file name (a bulk index of the server's titles, since its id filters are ignored), else title; by path where the path pairs match | titles, overview, genres, ids, studios, certification; People are not yet credits; music not wired |
| Audiobookshelf | audiobooks, books | ASIN or ISBN, else exact title | description, series, publisher, ISBN id |
| Navidrome | music | client built; by-path does not apply (its song paths are relative to its own folder) | not used in a scan; Jellyfin serves music |
| Calibre | books | identifier or title (`/ajax/search`) | shapes from the documentation only |
| Stash | adult | stash-box id | GraphQL |

Pictures: a Jellyfin server's posters and backdrops are offered in a series or film picker as source `local:jellyfin`, never chosen
over an external one. They are fetched through the connection with its own sign-in (the browser never sees a token) and cached like
any other provider picture; only the connection's own item-picture addresses are served this way.

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
| ListenBrainz | how many people listened to an album (by release group id) | yes | no key; shown as a rating on 0..10, two points a power of ten of listeners; enrichment only, no place in the order |
| Deezer | an album's fans, found by exact artist and title; explicit flag and cover are read but not yet shown | yes | no key; rating on the same scale; enrichment only |
| Apple Music (iTunes) | a large cover, found by the album's barcode; offered in the cover picker | yes | no key; asked only for an album that has a barcode; enrichment only |
| Anime id map (Fribb) | the AniDB, MyAnimeList and AniList ids of a TheTVDB series (TV and OVA entries of its list; films and specials left out), from one daily file on GitHub | tv | no key; off until switched on; fills only ids a series lacks, and they are kept with it |
| Last.fm | how many people listen to an album (and, for the Discover pages, similar artists) | yes | your own free key; off until it is stored; score by listeners on the same scale as Deezer; enrichment only |
| Discogs | the community's rating of a release, found by exact artist and title | yes | your own personal token; the 0-5 average is shown on 0-10; off until stored; enrichment only |
| Spotify | an album's popularity (0-100, shown on 0-10); cover and UPC are read, not yet shown | yes | your app as `client id:client secret` (client credentials); off until stored; enrichment only |
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
| TheXEM | scene numbering (TheTVDB's episode to the scene's), scene names | yes | no key; switched on or off, no place in the order ([tv-internals.md](tv-internals.md)) |
| Kitsu | anime synopsis, age rating, average rating, poster, and the MyAnimeList and AniDB ids | yes | no key; found through AniList's id (its `/mappings`), anime libraries, after AniList |
| MyAnimeList | an anime's mean score, synopsis and poster, by the MyAnimeList id Kitsu or AniList named | yes | your own client id; off until stored; asked at 60 a minute |
| AniDB | an anime's rating and description, by the AniDB id Kitsu named | yes | the client name you registered at anidb.net; off until stored; asked at one request every 4 seconds because AniDB bans faster clients |
| AniDB, MyAnimeList | anime titles, episodes, relations, ratings | no | AniDB bans heavy clients; MyAnimeList needs a client id |
| IMDb datasets | rating and votes | yes, **off** | IMDb's daily `title.ratings.tsv.gz`, streamed into `cache/metadata.db` by the task `imdb_ratings`; no key, no quota; personal, non-commercial use only. A series has one rating, so it fills where no earlier source holds one |
| fanart.tv | clear logos, banners, HD art, posters | yes | a free key; its posters are offered in the picker |
| OMDb | the series' IMDb rating and English plot | yes | a key; asked by IMDb id, last in the order; the per-season call is built, not used |

## Movies

| Provider | Carries | Built | Notes |
|---|---|---|---|
| TMDB | overview, cast and crew, releases and certification by country, studios, keywords, translations, **collections**, artwork | yes | a free key |
| Radarr's metadata service | TMDB's films with IMDb, Rotten Tomatoes and Metacritic ratings, release dates, collections | yes | no key; the fallback behind TMDB |
| Wikidata | the ids TMDB and IMDb know a film by | yes | first by default; no key |
| MDBList | ratings of IMDb, TMDB, Trakt, Letterboxd, Rotten Tomatoes, Metacritic in one call | yes | a free key, 1,000 requests a day |
| TheTVDB (movies) | English overview, genres, runtime, posters, backgrounds, banners, clear logos, remote ids | yes | Solarr's own key; found by IMDb id, else TMDB's |
| OMDb | IMDb, Rotten Tomatoes and Metacritic ratings, the full English plot | yes | a free key, 1,000 requests a day |
| fanart.tv | clear logos, clear art, disc art, banners, backgrounds (best liked, English first) | yes | a free personal key |
| Letterboxd, IMDb lists | lists to import from, not metadata | no | import lists, not built yet; AniList and TMDB lists are ([import-lists.md](import-lists.md)) |

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
| Hardcover | works, editions, series, ratings | yes | a key; what Bookshelf's service reads |
| Internet Archive | description, subjects, language, page count, cover | yes | no key; found by ISBN; last in the order |
| iTunes, FantLab | covers, descriptions | yes | Audiobookshelf's further sources |

**Wikidata leads what it holds** for books as for films: the overview and the year of first publication, and the
series, which Open Library does not keep. It never names the title: its title is the original language's, kept as an
alias. An audiobook's ISBN is its own reading's, so Wikidata's ISBN fills only a book's.

## Adult

Whisparr (v3) asks its own metadata server, which is ThePornDB behind a cache; Stash identifies scenes against
stash-box servers by name and by file fingerprint. Solarr asks both kinds in a library's order, Wikidata first.
StashDB, FansDB and ThePornDB speak the stash-box GraphQL schema, so `internal/metadata/stashbox` is one client with an
endpoint; each takes the owner's own key, sealed in `settings.db` and never shipped.

| Provider | Carries | Built | Notes |
|---|---|---|---|
| Wikidata | a performer's biography (the Wikipedia lead), reached through the item a stash-box server links them to | biographies only | first by default; holds no scenes |
| Whisparr's server (`api.whisparr.com/v3`) | scenes with credits, sites with their scenes, genres, images | yes | no key; routes `scene/search`, `site/search`, `site/{id}` |
| StashDB | scenes, performers (measurements, aliases, career, links), studios and networks, tags, fingerprints (oshash, phash, md5) | yes | a key; `searchScene`, `findScene`, `searchPerformer`, `findPerformer`, `findScenesBySceneFingerprints` |
| ThePornDB (`theporndb.net/graphql`, and `api.theporndb.net/movies` for films) | the same schema; its `queryScenes` wants `page`, `sort` and `direction` as strings, so Solarr uses `searchScene`; its scene filter is not supported, so a follow uses the others | yes | a key |
| FansDB | creators and fan-platform scenes | yes | a key; its server can fail a search with a database error, which is surfaced and not retried |

## Other media types (planned)

Comics: ComicVine, Metron, the Grand Comics Database (see [comics.md](comics.md)); games: IGDB, ScreenScraper. None is built; a media type is only as viable as its source
(ADR-42).

## How collections are found

| Library | Source | What it gives |
|---|---|---|
| Movies | TMDB collections | the films of a franchise, with the ones the library lacks to add |
| Movies | Wikidata ("part of the series", "media franchise") | a universe or franchise beyond TMDB's collections: Ihaol with Sfozutvaew and Romulus, the Marvel Cinematic Universe with its phases; kept only where it holds a film no single TMDB collection does (`movie.UniversePass`, daily) |
| Series | Wikidata claims (part of the series, media franchise, spin-off, follows) | the series of a franchise, with those the library lacks to add |
| Anime | AniList relations (sequel, prequel, spin-off, side story, parent, alternative) | the series of a franchise the library holds |
| Music | MusicBrainz series ("part of" a release group series), asked once per soundtrack, 120 requests a pass | the scores of one series, given the Wikidata item of the series (P1407) and the franchises it is part of (`music.CollectionPass`, daily) |
| Music | Wikidata, by release group (P436): the album's own series or franchise, or that of the film or show it is the soundtrack of (P406) | the franchise collection of the music library, under its widest item; a franchise reached through a soundtrack is kept for one album, as it links the scores with the films; one named on albums alone needs two |
| Books, audiobooks | Wikidata series (P179) of the books' Open Library ids | no new collection: each series collection (kept by name) declares its Wikidata item and the franchises it is part of, when it is named like the series or more than half its books are in it (`book.SeriesKeysPass`, daily) |
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

**Duplicates and links.** Collections made by different providers (or by Solarr and a provider) can be one collection said
twice. *Merge duplicates* on the Collections page lists the groups (`library.PlanCollectionMerges`, a pure rule) and merges each
into its largest: a collection whose works are all in another merges into it; one sharing at least two works with another under
a similar name (the words that are not "collection", "saga", "the" ...) and lacking at most one work, or a third of the larger's
size, whichever is more, merges too (6 works sharing 4 with a collection of 8 merge). Several fold into one at once; ids, links
and the monitored flag move to the larger. A collection of one library can be **linked** with one of another (a series' films
and the series): each then shows the other's works (`relationship` kind `linked_collection`).

**Across libraries.** A collection stays inside one media type, and the task *Link the collections of different libraries*
(`internal/service/franchiselink`, every six hours) links those of different types that a provider puts in one franchise,
without being asked: a series' Wikidata franchise and the same Wikidata franchise among a film collection's declared keys
(`collection_declared_key`: the franchises, and their parents, most of a TMDB collection's films are in, written by the movie
universe pass), a book series or soundtrack grouping that carries the same id, and a collection's own name as a TMDB keyword
(`keyword:<name>`, declared by the link pass itself when more than half its works carry the keyword its name is: TMDB tags a
franchise's films and shows alike; a keyword that is not the collection's name declares nothing), and an official TheTVDB list
(`list:tvdb:<id>`, declared by the link pass on a collection when more than half its works carry the list as a `franchise_list`
classification; the TV franchise pass writes those, below). When two media types name one franchise id, the
second keeps a sibling collection under `provider@media_type` so each library lists its own. A link shows the other collection's
works and what its library type lacks (add with the one Add dialog into that library), the reason it was made (a shared
`provider:id`, or by hand) and **Unlink**, which is remembered under the two collections' own ids (`collection_link_refusal`),
so no pass links them again; linking by hand forgets it. A work is in as many collections as name it. A series that joins a
family only by a crossover episode it shares is marked with that episode (`collection_member_origin`); taking it out of the
collection sticks like any dismissal.

**TheTVDB lists with films.** TheTVDB keeps one official list per franchise, and a list names films beside series. The TV
franchise pass (`FranchisePass.recordListFilms`) reads the official lists of up to 60 held series a pass, each again after a
month (`work_collection_check` source `tvdb-movies`), and resolves each film of a list holding 1 to 40 of them with one
TheTVDB lookup (`/movies/{id}/extended?short=true`: its TMDB id, else its IMDb id, names the library's film). The list is then
written whole: exactly its held series and films carry `franchise_list` value `tvdb:<id>`; the series read also loses the
lists it is no longer on. A pass starts no further series after 120 film lookups (the series begun is finished, so no series
waits on a budget it can never meet); a list a lookup failed on is not written and its series is asked again next pass. A
series or film in no collection is not linked this way (`franchiselink.Service.LinkShows` still links a film collection named for a series).
Undo is **Unlink**, remembered as for every provider link.

A film the library holds that TMDB lists in a collection, but whose own record does not name it, joins the collection the next
time the collection is opened (it was listed as missing before).

## When a provider fails

A provider's status is on Settings -> Metadata: **Failing** (five failures in a row, its circuit open), **Key rejected** (401 or
403) or **Paused** (its own rate limit or daily quota), each with when it is asked next. A stash-box server's GraphQL errors count
as failures though they arrive as 200. Metadata requests are all reads, so a dropped connection or a timeout is retried once after
half a second, except at Whisparr. When a circuit closes again, the task `identify_after_outage` retries the waiting files of the
libraries that provider serves, at most hourly per provider. Google Books ships switched off, since it answers 429 without a key,
and comes on when a key is stored.

## Your own providers (definition files)

A YAML file under `providers/metadata/custom/` describes a web service Solarr can ask for a book, a comic or a film; Settings >
Providers lists, tests and approves it, and a library adds it to its order on its Metadata page, where it carries a *Custom*
chip. It is asked after the providers before it and only fills what they left empty. Every request goes through the same
guard and pace as the compiled providers, to the hosts the file names and no other. See `provider-definitions.md`.

Music: a scan asks a Jellyfin server mapped to the library, once per album folder, for the MusicBrainz release-group (else release) id of the album at that path. It runs after the file's own tags and before any search or album matching, as the pass "Ids your media server holds for the album folder"; a file with its own MBID tags never asks. Import does not use it: a download's path is not the server's.

## Episode scores

With an OMDb key, a TV refresh reads each season's list (one request per season, at most 30) and stores every episode's IMDb score
in `work_part.rating`. The scores only fill episodes another source already lists; they never add one. A refresh that finds no
score keeps the stored one. The episode row shows it as a star beside the runtime.

A definition-file provider ordered in a TV library fills series-level fields only (title, overview, dates, genres, age rating, rating,
network, pictures, cast); it is asked by TheTVDB, IMDb or TMDB id, and its parts never become episodes.

A definition with `role: enrich` that declares `music` fills an artist's photo and biography where TheAudioDB left them empty, asked by
MusicBrainz artist id (namespace `musicbrainz`) while it is switched on. A definition ordered in an adult library fills a scene's title,
overview, date, studio, tags, pictures and credits; it is asked by a stash-box id (`stashdb`, `theporndb`, `fansdb`), else searched by the
scene's title and taken only on an exact title match.
