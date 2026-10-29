# Import lists

A library's import lists add the works a list names (ADR-38): an AniList season, an AniList user's list, a TMDB list,
TMDB's trending or popular shows. They are the third source of discovery, after what the library itself suggests and
the people it follows, and they are previewed before they are enabled.

## The pieces

| Piece | Where | What it does |
|---|---|---|
| List types | `internal/media/importlist.go` (`media.ListType`) | The keys of the sources this build reads |
| Sources | `internal/service/importlist` (`Catalogue`, `Kind`, `Source`) | Reads a list's entries for its settings, with the fields each type asks for |
| Resolution | `media.ListResolving`, TV's in `internal/media/tv/importlist.go` | Names the work an entry is, by an id the type's `AddWork` takes |

The list layer knows nothing of media types: an entry (`media.ListEntry`) carries the provider's ids, its title and
year and the provider's own word for what it is (`Form`: TMDB's `tv` or `movie`, AniList's `TV`, `ONA`, `MOVIE`, ...).
A media type takes import lists by implementing `media.ListResolving`: it names the list types its libraries offer and
turns an entry into the id it adds works by. A type without the capability offers no import lists.

## Sources

| Type | Reads | Settings | Bound |
|---|---|---|---|
| `anilist_season` | AniList's anime of a season, most popular first, adult entries left out | season (current, next, or a named one), year | 3 pages of 50 |
| `anilist_user` | A user's AniList list | user name, statuses (default watching and planning) | the whole list |
| `tmdb_list` | A public TMDB list (v3 `/list/{id}`) | the list's number or its themoviedb.org link | 25 pages |
| `tmdb_trending_tv` | TMDB's trending shows | today or this week | 5 pages of 20 |
| `tmdb_popular_tv` | TMDB's popular shows | none | 5 pages of 20 |

AniList needs no key. TMDB's lists use the owner's TMDB key (Settings, Metadata); without one a TMDB list fails with
"needs the TMDB key", it is never silently empty. Both go through each provider's own `httpx` bucket, the same as their
metadata reads.

A TMDB list may be given as a link. Its number is read from the link by pattern
(`https://www.themoviedb.org/list/<number>...`); the link itself is never fetched, so a list setting cannot make Solarr
call another host. A link to any other host is refused when the list is saved.

## TV's resolution

- A TMDB show (`Form` `tv`) is added by its TMDB id; a TMDB film is foreign to a TV library and left out.
- An AniList entry is searched at Skyhook by its English then its romaji title (two searches at most); the series whose
  AniList ids hold the entry's is added by its TheTVDB id. AniList splits a show into seasons and cours, Skyhook lists
  every one of them under the one series, so a second season's entry finds the series too. AniList films and music
  videos are foreign. An entry Skyhook does not list is unresolved, not an error.

## Not yet

Trakt (watchlist, user lists, trending) and IMDb lists are the next sources: each is one more `media.ListType`, a
`Kind` in the catalogue and its client; nothing else changes. Films from these lists wait for the movie type to
implement `media.ListResolving`.
