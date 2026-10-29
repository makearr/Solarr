# Books and audiobooks

Two media types, `book` and `audiobook`, over one module, `internal/media/book`, as Readarr ran one code base for
both. What differs is a `Format` (`format.go`): the media type and its words, the files a library holds, whether a book
is one file or a folder of chapters, the Newznab categories, the default provider order and the size floor. Everything
else is shared. Search, grab, RSS, the cutoff check, adding, placing files from Review and the refresh sweep are the
central implementation in `internal/media/works`, which TV and movies use too; the book module supplies only its
specialisation of each (`bookSpec`, `catalogSource`, the feed's matcher, its files for the cutoff). Storage is
`library.Series`, import `placer`, renames `renamejob`, covers `picturesweep`, as for movies.

| | Books | Audiobooks |
|---|---|---|
| Files | epub, azw3, azw, kfx, mobi, pdf, fb2, djvu, txt, rtf, lit, docx | m4b, m4a, mp3, aac, flac, ogg, opus, wma, mp4 |
| A book is | one file; another format is another version | a folder: its files are the chapters, numbered as parts |
| Newznab | 7020 Books/Ebook, 7000 Books | 3030 Audio/Audiobook |
| Providers, in order | Wikidata, Open Library, Google Books | Wikidata, Audible, Open Library, Google Books |
| Shipped profiles | Ebook: EPUB preferred; Ebook: any format | Audiobook: M4B preferred; Audiobook: any format |
| Upgrades | a better format replaces a downloaded one | none: a set of chapters is replaced by hand |

## Identity

A book's id is Open Library's work id (`openlibrary`, "OL27482W"); an audiobook's is its Audible ASIN (`audible`), with
the writing's Open Library id beside it. A work id names the writing, which a book and its audiobook share, and a
provider id belongs to one work, so an audiobook keeps the writing's ids under keys of its own (`openlibrary:audiobook`,
`isbn:audiobook`), as TMDB's films do with `tmdb:movie` (`Format.Provider`). Exclusions are kept by the same keys.

Authors and narrators (each its own level, Narrators opening the people list on that role) are people kept by their name (`book:person`, `PersonKey`: lower case, dots and spacing
normalised), so one writer is one person in every book and audiobook library, whichever provider named them.

## Fetching

`Service.FetchFor` asks the library's providers in its order, the first to hold a field supplying it (ADR-8):

1. Audible first when an ASIN is known: its ISBN, title and author find the writing.
2. `completeIDs`: Open Library's work from an ISBN, else the one search hit with exactly that title by that author.
3. `bridgeIDs`: Wikidata's item, found by Open Library's id (P648), adds Goodreads, Google Books and, for a book, ISBN.
4. Every provider in order; one that lacked the id it needs (Google Books, by an ISBN Open Library answered) is asked
   in a second round.

Wikidata leads its overview (the English Wikipedia lead), first publication, language, authors and series; never the
title. Open Library's subjects become genres after its bookkeeping tags ("Accessible book") are dropped, mapped onto
the `books` genre tree (`genretree.go`), which both types share. A series is the work's franchise (`book:series`, by
the series' name, so Audible's several ASINs for one series are one collection; the refresh sweep merges same-named ones)
and the book's place in it is the `series_position` field.

An author's page lists their other books from Open Library (`AuthorWorks`, `profile.go`), each added to the library from
there; covers come from Open Library, Google Books and Audible, whose hosts the picture cache allows.

Three more providers, found by reading what ReadMeABook, Listenarr, Calibre-Web and Audiobookshelf use: iTunes (audiobooks
only, keyless), FantLab (keyless; the original title becomes the title, the Russian one an alias) and Hardcover (needs the
owner's token, GraphQL). Each is asked by its own id, else by the title and author another source learned, taking the one
hit whose title and author agree. A library that saved its own provider order keeps it; the new ones are added in its
Metadata settings. Not built: Amazon, Douban, Lubimyczytac and Google Scholar (HTML scraping), ComicVine (comics).

## Files

The scan (`consumer.go`) and import (`import.go`) read a book from its path (`internal/identify/parsebook`):
"Author - Title (Year) [EPUB]" (ebook formats: epub, epub3, cbz, azw3, azw, kfx, mobi, pdf, cbr, fb2, djvu, lit, pdb, pml, rtf, txt, html, docx; EPUB and CBZ are the preferred tier, M4B the audiobook one), "Title by Author", scene names with dots, and, for an audiobook, its folder
("Author/Title (2015)/01 - Chapter One.mp3"). An ebook is media to a book import only: the walk counts ebook files as
media when the importer says so (`batch.BookFiles`), so a PDF booklet beside an album stays an extra.

The resolver takes an id in the name (ISBN, ASIN), else a title the library holds, else the provider's exact title by the
named author among its first three hits, which it adds; anything less is left for Review.

## Searching

`Search` asks Newznab's book search by author and title first, then "Author Title", then the title alone. A release is
the book when it names one of its titles and one of its authors as whole words (`parsebook.Names`, Readarr's
search-criteria match), and is a file of the format it claims: an M4B release is no ebook. What the name claims (its
format as the file's extension, an audiobook's bitrate) stands in for the file until it arrives (ADR-39).

## Naming

Readarr's two slots, Author Folder Format and Standard Book Format, with its tokens (`{Author Name}`, `{Book Title}`,
`{Book Series}`, `{Book SeriesPosition}`, `{PartNumber:00}`…) and, for audiobooks, `{Narrator Name}` and `{ASIN}`. A
book in one file has no part, so `[ - {PartNumber:00}]` renders nothing for it. The Audiobookshelf preset writes the
layout Audiobookshelf reads: "Author/1 - Title {Narrator}/01 - Title.m4b".

## Authors and series

**Following an author** (`follow.go`, Readarr's author monitoring) offers the author's books at Open Library, the list the
author's page shows: those first published from the year of the follow on, or all of them for a back-catalogue follow,
5 a run (25 for the back catalogue), each added monitored and never past an exclusion. Only the author role is
followed: Open Library lists books by author.

**A series** is a collection (one per series name). Its missing books (`series.go`) come from Wikidata: the series the
held books' Open Library works are part of (P648, P179), its books in series order (P1545) with their Open Library work
ids; a book held under any of its ids, excluded, or with no Open Library work id is not listed. Monitor collection adds
the missing ones.

## Not built

OPF or metadata.json files beside a book, and import from Readarr. A monitored series does not yet add a book published
after it was monitored (no periodic pass, as for films).
