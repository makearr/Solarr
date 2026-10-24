# Which library a work belongs in

Adding from anywhere suggests a library with its reason (ADR-56, plan 10-libraries L5); a library's check lists what
it holds that belongs elsewhere.

## Facts

A lookup candidate carries `media.Traits`: its genres as the source names them, original language, origin countries,
whether AniList knows it, its certification. TV takes them from Skyhook's search records (TMDB's hits for shows only
TMDB lists), films from TMDB's hits (`genre_ids` read through `tmdb.GenreNames`) or Radarr's service. A held work's
facts are read from the library: its genre tags, `original_language`, the `original_country` field, an AniList id, its
certification's age (`library.Works.TraitsInCategory`).

## Rules

A library suggests itself when every condition of one of its rules holds (`settings.LibraryRule`): a genre (a genre
below it matches too, through the taxonomy), an original language or a country (ISO codes, two- or three-letter), or
AniList. A library states its own rules (`category.rules_json`, Settings tab "Suggested for", advanced) or follows its
kind's shipped ones (`media.LibraryKind.Rules`, `internal/media/screen/kinds.go`):

| Kind | Rules |
|---|---|
| Anime | Animation and Japanese original; Animation from Japan; known to AniList |
| Documentary | Documentary |
| Kids | Kids; Family |
| Reality | Reality |

A further media type of a library uses the library's rules and kind.

## Choosing

`adapters.Suggester`: among the libraries holding the work's media type, a library whose maximum rating a known
certification exceeds is left out; the rule matching the most conditions wins, library priority breaking a tie. No
rule matching anywhere suggests nothing. The API only suggests a library the caller may add to.

## Where it shows

- Add from the dashboard preselects the suggested library, its reason beside it; a library's own Add says "Fits Anime
  better" with a link.
- Files -> **Belongs elsewhere** lists the library's works whose rules point to another library, to move or copy one
  or all (L3), and a kind of library none exists of although works here would go there ("No Documentary library").
