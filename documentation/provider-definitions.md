# Provider definitions

A provider can be a YAML file: what Solarr asks and how to read the answer, never code it runs. The files live under
`providers/` in the config folder:

```
providers/
  README.md                       written at every start
  metadata/builtin/*.yaml         a readable copy of what ships; rewritten at every start, an edit here is overwritten
  metadata/custom/*.yaml          yours; the same key replaces the built-in one
  indexers/ download/ webhooks/   the same two folders each
```

Built-in definitions are always read from the program itself; `builtin/` is only a copy to read and copy from. Only `custom/`
is read from disk, at start, on Reload and after a save in Settings > Providers.

## The envelope

Every file starts with `schema: 1`, `kind` (`metadata`, `indexer`, `download`, `webhook`; must match the folder), `key`
(`[a-z0-9_-]{2,40}`, must equal the file name), `name`, `engine` (`builtin`, `http` or `spec`), `enabled` and `description`.

- `engine: builtin` describes a provider that is compiled in. A custom file with its key may change `enabled`, `pace.per_minute`
  (never above the compiled maximum) and narrow `media_types`; anything else it changes is shown and ignored.
- `engine: http` (metadata, webhook presets) and `engine: spec` (indexers) are providers that are entirely the file.

## Metadata, `engine: http`

`media_types`, `library_kinds` (optional), `hosts` (every host it may reach), `pace` (`per_minute` required), `settings`
(values the owner fills in; `password` ones are sealed and never written to a file), `headers`, `cookies`, `ids.own`, a `search`
operation and an `item` operation (and `by_id`). An operation is a `request` (or up to eight `steps`), the `response` kind
(`json`, `html` or `xml`), `rows` and `fields` that read the generic record (`id`, `title`, `year`, `genres`, `tags`,
`page_count`, `rating`, `adult`, ...), and the sections `parts`, `people`, `images` and `organisations`. Values are read with
the indexer engine's selectors and filters. The files under `provider-definitions/examples/` are complete examples; the two
ports (iTunes audiobooks, FantLab) are tested against the compiled clients and give the same values.

A media type uses a definition only when its module can merge a generic record (`media.RecordConsuming`). Comic, book and
audiobook, and movie libraries can; TV, music and adult libraries refuse a definition that names them, with a warning in the
log. A definition is never first for identity unless a person puts it first in the library's order, and it only fills what the
providers before it left empty. A book or movie definition fills a work the compiled providers found; it is not offered in the add search, since a candidate needs one of the type's own ids. Where the library's ids are typed fields (book, movie) a definition's own id is not stored: a
book is asked again by title, a movie by its IMDb or TMDB id through the definition's `by_id`. An `adult: true` definition is
offered only to libraries of an adult kind (or of the adult type). `cache: {search: 24h, item: 168h}` keeps answers in memory
for that long (a failed answer is never kept; 256 answers at most).

A custom descriptor of a compiled provider (`engine: builtin`) may switch it off, set `pace`, narrow a metadata provider's
`media_types` (the library picker then leaves it out of the others), name `base_urls` for an indexer kind (the add list shows
them, the first the default) and a `request_delay` that floors the gap between an indexer's requests.

`via: flaresolverr` sends the requests through the owner's own FlareSolverr; the definition declares a setting named
`flaresolverr_url`.

## Download clients, `engine: http`

A definition says how to talk to an external usenet client; Solarr implements no client. Paths are relative to the address
the owner enters (a path that is absolute, scheme-relative or carries `@` is refused), so a definition never names a host.
`protocol: usenet` only (torrent definitions are not offered). The secret travels by `auth` (`query` with a name, `header` with a
name and prefix, or `basic` with `user:password`).

Parts, all required: `probe` (steps, then `version`, optional `download_folder` and `categories`), `add` (a request with
`file_field`, answering `ref`, `ok`, `error`), `downloads.list` (one listing per place the client keeps downloads: `rows`, fields
`ref name status size left category path message`, `states` and a required `default_state`) with optional `by_ref` listings,
`queue_status` (`paused speed items left`), and `remove`, `pause`, `resume` (steps; a step fails when its `error` reads
non-empty). Templates see `.Query.name`, `.Query.category`, `.Query.refs` (comma-joined), `.Query.delete_files`, `.ID`,
`.Settings.secret` and `.Step.<name>.<field>`; a query value that renders empty is left out. The `scale` filter turns a
decimal into whole units (`"1048576"` turns megabytes into bytes). `blank_when` empties a field when the row matches a
selector. A custom definition is inactive until an administrator approves it in Settings > Providers; a key equal to a
compiled client's impl is ignored. The SABnzbd port in `internal/acquiring/specclient/testdata/sabnzbd.yaml` is the worked
example, and a test asserts it sends the compiled client's requests and reads the same answers.

## Safety

| Rule | How |
|---|---|
| No code | Templates and filters are the indexer engine's fixed set; regular expressions are RE2 |
| Hosts are declared | A request or redirect to any other host is refused before it leaves; declared hosts still pass the SSRF guard (public addresses only) |
| Custom files start inactive | A custom `http` or `spec` file is **Needs approval**: an administrator sees its hosts and enables it. The approval stores the file's hash and its host list; a changed host list needs approval again |
| Secrets | Values are sealed in `settings.db`, sent only to declared hosts, never returned by the API |
| Bounds | file at most 256 KiB, 1,000 custom files a kind, 8 requests an operation, answers at most 8 MiB, 15 s a request, a pace is required |
| A bad file harms only itself | Errors are per file and listed with line and column; start-up never fails on a definition |
| Nothing deleted | Revert and Remove move the file to `custom/.removed/<key>.<unix time>.yaml` (the newest 50 are kept) |

## Settings > Providers

Administrators, advanced tier. Tabs per kind; each row shows origin (built in, custom, overridden), state (on, off, needs
approval, error) and the actions: enable or disable, test (a search for a title and the first item, with the requests made),
copy to custom, edit (problems listed by line), revert, remove, and the keys of a definition that asks for settings. A library's
Metadata page lists an active definition marked **Custom**; one that is gone shows as no longer available and is skipped.

The API is under `/api/v1/provider-definitions` (see `api/openapi.yaml`).
