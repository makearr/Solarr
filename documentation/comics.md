# Comics and manga

> **Level: 🟡 Advanced.** The comic media type: Comics, Manga, Adult comics and Hentai libraries.

One media type, `comic` (`internal/media/comic`), with four library kinds. A series is a work; its parts are issues
(Comics, Adult comics) or volumes (Manga, Hentai). It is shaped like TV: a series holds one wanted item per kind of part
(`issue`, `annual`, `special`, `volume`, `chapter`), as a TV series holds one per season, and a file covers parts of the
item its kind names. Adding, refresh, the picture sweep, Review and the scan use the shared `works` machinery.

| Kind | Reads | Providers, in order | Adult |
|---|---|---|---|
| Comics | series of issues | ComicVine, Metron, Grand Comics Database | no |
| Manga | series of volumes | AniList, MangaUpdates, MangaDex | no |
| Adult comics | series of issues | ComicVine, Metron, Grand Comics Database | yes, confirmed when the library is created |
| Hentai | series of volumes | AniList, MangaUpdates, MangaDex | yes, confirmed when the library is created |

## Providers

All five comic providers ship switched off (Settings > Metadata). ComicVine takes your own key; Metron takes your account
as one secret `user:password`. The Grand Comics Database, MangaUpdates and MangaDex need none. AniList also serves comic
libraries. A result a provider flags as adult (AniList `isAdult`, MangaDex content rating erotica or pornographic) is
offered only to the two adult kinds and refused by the others.

- **ComicVine**: 200 requests an hour for each kind of request; a series' issue list is read a hundred issues a request,
  and the refresh sweep stops after twenty series a run so the budget is never exceeded.
- **Grand Comics Database**: <https://www.comics.org>, data under CC BY-SA 4.0. A library that uses it credits
  comics.org. Solarr reads series facts only; it does not list a series' issues from it.
- **MangaDex** is metadata only: Solarr never downloads chapters from it. Its volume-to-chapter map decides which volume
  holds a chapter. Credit MangaDex and MangaUpdates where their data is shown.

## Identification

A scan reads each archive (cbz, cbr, cb7, cbt, pdf) once: page count, median page size, image format, and the embedded
`ComicInfo.xml` or `MetronInfo.xml`. The series is found in this order, each step a piece of evidence with a confidence:

1. a ComicVine or Metron series id in the file's own metadata;
2. a held series whose title or alias is the name in the file, folder or parent folder, with a year that agrees (within one);
3. a provider search; one series of that title is added at the 0.90 threshold (a year or publisher that agrees lifts it),
   two series of one title and no year to tell them apart are not guessed between.

Anything below the threshold waits in Review with the candidates. A series added by a scan is unmonitored: its files, not
the provider's whole back catalogue, are what you asked to keep; a file's issue becomes wanted once it has one.

A file naming a range (`001-006`, `v01-v03`, `c040-047`) covers every part of it. A manga chapter file counts toward the
volume the provider lists it in; a chapter no volume holds, and a volume or chapter of a series whose count no provider
states, becomes a part when its file is found.

## Where the facts are

A file's median page edges are stored as its width and height, its page count and image format as claimed attributes, so
a quality target can compare them. Unknown size (a PDF, avif pages) never fails a gate.
