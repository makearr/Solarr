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
libraries. A result a provider flags as adult (AniList `isAdult`, MangaDex content rating pornographic; MangaDex rates much mainstream manga "erotica", which is not adult) is
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

## Library options

A media type declares choice settings in its descriptor (`FileSettings` with options); a library stores the picks as module options (`category_file_management.module_options`) and File Management shows them under "Library options". The comic type declares three:

- **Date type** (`date_type`: store or cover): which date dates an issue (`work_part.date`, `available_at`, the pull list, when it is searched for). It applies when a series is added or refreshed.
- **A collected edition satisfies its issues** (`edition_satisfies_issues`, default off): see below.
- **Kavita and Komga files** (`reader_files`: off, fill, replace): see below.

## Releases

A release is wanted when it names the series (or an alias; a comic's year must agree within one, a manga volume's year is its own edition's) and a wanted part, alone or in a range. A title ending in Complete, Complete Series or Collection counts for every wanted part. A release whose size cannot be its parts (under 1 MiB a part, over 2 GiB for one) is rejected with the rule `implausible-size` and the sizes. Queue and history rows say what a release holds: `#12`, `#1-#6`, `v03`, `c120`, `Complete`.

## Collected editions

`part_coverage(covering_part_id, covered_part_id, source)` says a held part stands for another: a trade paperback (its own work with a `whole` part) for the issues it collects. A row counts only while the covering part has a file on disk. With the library setting on, a covered issue is not searched for; the series page shows it as held in the edition (`in_edition` on a part). Rows are filled by a provider or by hand (`library.Coverage.Replace`; source `hand` is never replaced by a provider). The edition's page has a **Covers** dialog (`GET`/`PUT /works/{id}/coverage`, administrator): find a series, pick a range of its parts, save; provider rows show but are read only. A Format filter on the library list (trade paperback, omnibus, ...) is the Trades view. Not built: Metron's reprint data filling rows.

## Reading lists

On a comic library's Story arcs page, *Import reading list* takes a ComicRack `.cbl` file (at most 1 MiB, `encoding/xml`, no external entities), matches each book to a held series (by title or alias, narrowed by the volume year) and issue, and previews matched, ambiguous and not-held books (`POST /categories/{id}/reading-list`, nothing saved). Creating the list makes an ordered crossover collection of the matched issues.

## Files for readers

With *Kavita and Komga files* on, a daily pass (and one when the setting is switched on) writes `series.json` beside each series' files and a `ComicInfo.xml` into each cbz: fill adds the elements a file lacks and never changes one it has, replace sets every element Solarr has a value for. A manga library writes `Manga` as `YesAndRightToLeft`. A cbz is rewritten only when the result differs, entry by entry with the compressed bytes untouched, through a copy swapped in whole; nothing is written on a read-only or offline root. The old bytes of a rewritten cbz go to the root's recycle bin first (reason Replaced, the library's retention); if that fails the file is left as it was. A pass also runs for a series right after a download import placed files of it.

## Naming of ranges

A file holding several volumes or issues is named for both ends: `v04-05`; `{Volume:00}` pads both.

The Kavita and Komga files setting has a **Preview** button beside it in File Management: `GET /categories/{id}/file-settings/{key}/preview?option=` runs the pass in that mode without writing (a media type declares which settings can, `media.FilePreviewing`) and answers how many files would be written. It opens every cbz of the library once, one after the other.

## Grand Comics Database issues

GCD lists a series' issues by address only, so a refresh reads them one request each on the provider's pace (at most 60 a refresh, two minutes at 30 a minute,
variant covers left out). It reads them only for a series no other source gave issues for; ComicVine's and Metron's lists win.
Partial dates GCD writes with zeros ("1963-03-00") are kept as the part of the date it knows.

## Following a creator

A writer, artist or other credited creator on a comic page can be followed like an author. The series come from ComicVine (it needs your key): the person is
found by their exact name, their volume credits are read, and the series come in one batched request. A follow adds the series that started from the year it
was made, monitored; a back-catalogue follow adds every one (at most 5 a run, 25 for a back-catalogue). A name ComicVine spells differently finds nothing.
