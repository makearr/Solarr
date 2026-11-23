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

A media type uses a definition only when its module can merge a generic record. Comic libraries can; the others refuse a
definition that names them.

`via: flaresolverr` sends the requests through the owner's own FlareSolverr; the definition declares a setting named
`flaresolverr_url`.

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
