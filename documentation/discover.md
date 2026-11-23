# Discover

> **Level: 🟡 Advanced.** What your libraries do not hold yet, from the charts of their sources.

Discover shows each library a poster wall of works it does not hold (or holds but does not monitor), taken from the charts
of the sources behind its media type. Everything is off until the owner switches it on under **Settings, General** and then
enables it per library under **Settings, Discover**. A dashboard row shows the most popular of your libraries.

## Sources

| Library | Source | Lists |
|---|---|---|
| Film | TMDB (needs the TMDB key) | trending, highest rated, upcoming, classics |
| TV | TMDB; an anime library reads AniList instead (no key) | trending, highest rated, upcoming, classics |
| Music | Last.fm (needs the Last.fm key); names are resolved to MusicBrainz by exact name | trending artists and albums, artists and albums alike the ones you hold most of, classic albums of your genres; no upcoming (Last.fm has no calendar) |
| Ebooks | Open Library (no key) | trending this week, highest rated (at least 100 ratings), classics (at least 300 ratings, first published long enough ago); no upcoming |
| Comics and manga | AniList manga (no key); Western comics have no free popularity chart | trending, highest rated, upcoming, classics |
| Any library of a media type with collections | the provider that names the collection | collections: the members of a collection you hold one of |

A list whose source cannot be read (no key, source down) keeps its old items and says why under Settings, Discover.
Audiobooks, adult libraries and magazines have no feeds yet.

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

- the source's rating is at least *minimum rating* (skipped for a source with no ratings, Last.fm);
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
