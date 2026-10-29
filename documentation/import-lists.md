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

## Settings

`settings.db` `import_list` holds a list's configuration only: its library, type, settings by field key as JSON
(`options`, migration 0057), cadence, monitoring (`monitor`, and the media type's `monitor_option`, empty for the type's
default), quality profile (empty for the library's) and exclusions. Exclusions are titles or `provider:id` (such as
`tmdb:1399` or `anilist:21`), separated by commas or lines; a matching entry is left out. What a list did lives in
`library.db` (migration 0099): `import_list_entry` and `import_list_state`, keyed by the list's id without a foreign
key, as every settings id is.

## The sync

The task `import_lists` ("Add what import lists name", ADR-59) enqueues `importlist.sync` hourly; the sweep runs each
enabled list whose `cadence_hours` have passed since its last run. One list's run (`action_key`
`importlist.sync:<id>`) is the same job scoped by its payload, so a sweep and a run of the same list never queue twice.

A pass reads the list and judges each entry, in order, once per provider id:

| Verdict | When | Kept |
|---|---|---|
| `handled` | the list added it, found it held or found it foreign before | — |
| `filtered` | the list's exclusions name its title or an id | no, checked again each run |
| `excluded` | a person removed the work and asked not to see it again (`work_exclusion`, this media type only) | no, so **Allow again** lets it in |
| `held` | the library has the work, by any of the entry's ids or the one it resolved to | `held` |
| `foreign` | the media type says it is not its own (a film on a TV list) | `foreign` |
| `unresolved` | no source names the work | `unresolved`, looked up again after a week |
| `unchecked` | past the bound on lookups by name: 30 a preview, 100 a run | no |
| `add` | none of these | `added`, once it was added |

A run adds at most 50 works, each through the media type's own `AddWork` with the list's monitoring and profile, in the
library the list belongs to; the rest wait for the next run. Kept entries are never added again by that list, even
after the person deletes the work: a dismissed work stays dismissed. An outage of the list's source or of the type's
add stops the run, keeping what it did; an entry the type refuses to add is kept as unresolved. The run records when it
ran, how many works it added and why it stopped (`needs_key`, `list_not_found`, `source_unavailable`, `not_supported`,
`failed`), which the Import lists screen shows. A run starts no search; the works it adds are hunted like any other
monitored work.

## Preview before enable

ADR-38 has every external list previewed before it adds anything. A list is created disabled (`POST
/libraries/{id}/import-lists` with `enabled: true` answers 409). `POST /import-lists/{id}/preview` runs the same pass a
sync runs, with the lookup bound of a preview, and answers every entry with its verdict (and, when it was looked up,
the id it would be added by); it adds nothing and keeps no entry. It records one thing: the list's type and settings as
previewed (`import_list_state.previewed_options`, compared after the settings are put in one canonical form).

`PATCH /import-lists/{id}` with `enabled: true` is refused (409) unless the type and settings it saves are the ones
last previewed. Changing a list's cadence, monitoring, profile or exclusions keeps it enabled; changing its type or
settings needs another preview, so the screen saves such an edit disabled and asks for the preview again. `POST
/import-lists/{id}/run` queues an enabled list's own run at once (409 for a disabled one). A preview that cannot read
its list answers 502 with what to do (add the TMDB key, check the list exists), and one the library cannot take 409.
`GET /libraries/{id}/import-list-types` lists the types the library's media type takes, with each type's settings as
fields, and the type's monitor options. Deleting a list forgets its entries and state; the works it added stay.

## The screen

A library's Settings, Import lists (`web/src/app/ImportLists.tsx`, its dialogs in `ImportListDialogs.tsx`, the pure
rules in `web/src/lib/importLists.ts`): one row per list with its type, settings, cadence, whether it is enabled and
its last run (or why it failed); Preview, Enable or Disable, Run now, Edit and Remove. The add and edit dialog draws the
type's fields from `import-list-types` (text, one choice, several choices, a year), so a new source needs no screen
change. Saving a new list, or new settings, saves it disabled and opens the preview; the preview lists every entry
(virtualised) with its verdict and counts by verdict, and offers **Enable this list**, which enables it and queues its
first run. Editing only the cadence, monitoring, profile or exclusions keeps an enabled list enabled.

## Not yet

Trakt (watchlist, user lists, trending) and IMDb lists are the next sources: each is one more `media.ListType`, a
`Kind` in the catalogue and its client; nothing else changes. Films from these lists wait for the movie type to
implement `media.ListResolving`.
