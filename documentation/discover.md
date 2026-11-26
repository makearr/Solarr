# Discover

> **Level: 🟡 Advanced.** What your libraries do not hold yet, from the charts of their sources.

Discover shows each library a poster wall of works it does not hold (or holds but does not monitor), taken from the charts
of the sources behind its media type. Everything is off until the owner switches it on under **Settings, General** and then
enables it per library under **Settings, Discover**. The very first switch-on enables every library once (recorded, so it
never repeats); a library you switch off afterwards stays off. A dashboard row shows the most popular of your libraries.

## Sources

A library reads every source of its media type whose provider is switched on under **Settings, Metadata** (and has its key,
where it needs one). Each list's size is shared between the sources that read it, so a second source never makes a list
longer. An item two sources name by the same id is shown once. Each source's popularity is scaled to 0 to 100 against the
highest it answered, so the sources interleave in the poster wall instead of one source's larger numbers putting it on top.

| Library | Sources | Lists |
|---|---|---|
| Film | TMDB (needs the TMDB key) | trending, highest rated, upcoming, classics |
| TV | TMDB | trending, highest rated, upcoming, classics |
| Anime (a TV library of the anime kind) | AniList (no key); TMDB's Japanese animation (needs the TMDB key; trending is the most popular, as TMDB's trending chart takes no filter; a show is keyed by its TheTVDB id, so it merges with AniList's entry) | trending, highest rated, upcoming, classics |
| Music | Last.fm (needs the Last.fm key; names resolved to MusicBrainz by exact name): trending artists and albums, artists and albums alike the ones you hold most of, classic albums of your genres. ListenBrainz (no key, MusicBrainz ids): the week's most listened artists and albums, upcoming albums of the next 30 days. Deezer (no key; the chart of the country the instance runs in, names resolved like Last.fm's): trending artists and albums | as named per source; nothing carries ratings |
| Audiobooks | Audible catalogue (no key; the library's region store): best sellers, highest rated, upcoming. Open Library and Hardcover as for ebooks | as named per source |
| Ebooks | Open Library (no key): trending this week, highest rated (at least 100 ratings), classics (at least 300 ratings, first published long enough ago). Hardcover (needs the Hardcover token): most read in the last 30 days, highest rated (at least 500 ratings), upcoming (most awaited first), classics (at least 300 ratings); a book is keyed by the Open Library work its ISBN names, so it merges with Open Library's entry, else by its Hardcover id | as named per source |
| Adult | every stash-box server with its API key: StashDB, ThePornDB, FansDB | trending, upcoming (scenes dated after today); no highest rated or classics, stash-box servers keep no ratings |
| Comics and manga | AniList manga (no key); Western comics have no free popularity chart | trending, highest rated, upcoming, classics |
| Any library of a media type with collections | the provider that names the collection | collections: the members of a collection you hold one of |

A source that cannot be read (no key, source down) keeps its old items and says why under Settings, Discover, one line per
list and source; the other sources of the list go on. Magazines have no feeds yet.

## What is kept out

A recommendation is shown only if all of these hold, and the auto-add preview names each one that fails:

| Reason | Meaning |
|---|---|
| Already in the library | you hold it and monitor it |
| Excluded | it is on the library's exclusion list |
| Genre or network excluded | on the global or the library's never-recommend lists |
| Language | its original language is not one the library recommends (defaults to the library's language profile; an item with no language is not judged by it) |
| Age rating | above the library's rating limit |
| Another library | the libraries' suggestion rules send it to a different library of the same type |

Not interested hides a title for you only, in every library of yours; **Hidden** lists what you hid, with Restore, and an Undo
appears for a few seconds after hiding. Load more reads the next page from the source.

## Automatic adding

Per list and library, **Add automatically** adds what passes both gates after every refresh, most popular first:

- the source's rating is at least *minimum rating* (skipped for a source with no ratings: Last.fm, ListenBrainz, Deezer, stash-box; an unrated item of a list that also has an unrated source is taken to be that source's);
- it has at least *minimum votes* (Last.fm gives albums no vote count, so albums need the floor at 0);
- no more than *at most per day* are added to a library in 24 hours;
- never an item automatic adding added before, even if you deleted it.

Added works are monitored; *Search for what auto-add adds* also starts a search, unless the mode (read only, observe,
*Arr companion) stops searches. Switching Add automatically on first shows what it would add now and why it holds the rest back
(`POST /settings/discover/libraries/{id}/preview`); nothing is added by looking.

## Refresh

A daily task refreshes every enabled library; saving a library's settings refreshes it at once. A refresh replaces a list's items,
so Load more reads are kept until the next one.

## Replaces

The TMDB trending and popular import lists no longer exist: an upgrade turns each into Discover settings (trending, adding
automatically when the list was enabled; popular becomes trending), deletes the lists and the unused discovery jobs table.
This cannot be undone.

## The page of an item the library lacks

A card for something the library does not hold links to `/library/{id}/external/{provider}/{id}`. The page asks `GET /works/describe`, which goes through the media type's `media.Describing` capability (the add catalog's source implements `works.Describer`): the provider's merged view as a `WorkPreview` with facts (keyed, worded by the interface), genres, ratings on a ten-point scale, cast and crew, and parts grouped the type's own way (episodes by season, tracks by disc, issues in one group). Nothing is stored. A type or kind that cannot describe (a music artist) answers 400 and the page shows the lookup entry alone, with Add and Ignore.
