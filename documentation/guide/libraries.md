# Libraries: organising what you have

> **Level: 🟡 Advanced.** Everything here is optional once a single library works. Pages each section builds on
> are linked from it.

## Which library does a work go in? (🟡)

Adding from the dashboard picks the library the libraries' rules suggest (an anime goes to Anime: "Animation,
original language ja"). The reason is always shown and the choice is changeable. A library's own Add says when
another fits better.

- A library's kind brings its rules (Anime, Documentary, Kids, Reality). Its settings' **Suggested for** tab
  (advanced) changes them.
- **Files → Belongs elsewhere** lists what a library holds that belongs in another, to move there, and says when
  a kind of library is missing ("No Documentary library: 4 here would go there").

Reference: [library-suggestions.md](../library-suggestions.md).

## Move to library (🟡, administrator)

On a work's page, or on a selection in the list. It moves or copies works to another library of the same kind (a
series to Anime, say). The preview shows every file's new place under that library's naming, with the subtitles
and pictures that come along. A copy is unmonitored in the other library and shares the file's bytes where the
disk allows.

## A library of several kinds (🟡)

**Settings → Libraries → Also holds…**: an Anime library can hold movies as well as series. Each kind has its
own root folders, quality, naming and monitoring (Edit on its "also holds" row). Name, priority, maximum rating
and who may see it belong to the library.

The library is one entry in the tree: **All** lists every kind with a Series/Movies chip, each kind's own pages
follow, Add switches between the kinds, and Wanted, Insights, Files and Settings have a tab per kind.
Reference: [mixed-libraries.md](../mixed-libraries.md).

## Maximum rating (🟡)

**Settings → Libraries → Edit**, for TV and movie libraries. A Kids library set to "13 (PG-13)" keeps anything
rated higher, or unrated unless you allow it, where it is but never searches for it there. The list's **Above
rating** button shows those, their pages say so, and a notice counts them. Reference:
[rating-limit.md](../rating-limit.md).

## Tags and genres (🟢 to use, 🟡 to edit)

**Settings → Tags** shows every genre as a tree, one per kind of media (Music; Film and TV), and your free
labels, with how many items carry each. An administrator renames, moves a tag under another, merges two (the
merged name keeps leading there when a provider uses it again), adds or deletes them. Reference:
[tags.md](../tags.md).

## Collections and franchises (🟡)

**Collections** (a library's Collections level) holds the providers' collections and franchises beside your own.

- One a provider or Solarr made can be renamed, deleted, filled with a work by hand or have a work taken out of
  it. None of that comes back undone on a refresh.
- **Merge duplicates** (administrators) shows the collections that are one said twice and folds each group into its largest; **Link** joins a collection with one of another library so each shows the other's works. Collection posters carry the same coverage bar as artists, and a view (search, order, favorites) can be saved by name.
- **Monitor collection** on a provider's collection adds the works of it you lack. The task **Add the new works
  of monitored franchises** (every 12 hours, at most 25 works a run) adds what the provider lists later: films,
  series and books alike.
- On one you made, it monitors every work in it, and every work you put in it while it stays monitored.

## Saved filters (🟢)

**Filter** (Series, Cast, and the Artists and Albums levels) is Sonarr's custom filter: build conditions (field,
operator, value) that must all hold, optionally name and save them, and pick a saved one from the menu. The
active filter is in the address, so a link keeps it. A **Person** filter beside Tags narrows a library's list to
one person's works (type-ahead over people).

## Quality, naming and files per library (🟡)

| Setting | Page |
|---|---|
| Ordered quality profiles, cutoff and target | [library-quality.md](../library-quality.md) |
| File and folder names | [naming.md](../naming.md) |
| Recycle bin, file dates, permissions, importing extra files | [file-management.md](../file-management.md) |
| Lists that add series by themselves | [import-lists.md](../import-lists.md) |
