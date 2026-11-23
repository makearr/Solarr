# Webhooks

## Outgoing

Connections > Webhooks (administrators, advanced tier): add a webhook with a name, a preset, the receiver's address, an optional secret, the events and the libraries (none
selected is every library). The address and the secret are sealed at rest and never returned; the list shows the address's host.

Events: `grabbed`, `imported`, `upgraded`, `import_failed`, `download_failed`, `renamed`, `file_recycled`, `work_added`,
`work_removed`, `request_created`, `request_approved`, `health_raised`, `health_cleared`, `test`. `imported`, `upgraded` and
`file_recycled` are recorded where the media servers are told of a placed, changed or recycled file; `grabbed` where the
download history records a grab; `download_failed` and `import_failed` when a download becomes FAILED (an import that found
nothing usable is `import_failed`); `renamed` when a rename job finishes; `work_added` and `work_removed` at the add and
remove actions of the API; `request_created` when a request waits for a decision and `request_approved` when one is approved;
`health_raised` and `health_cleared` when the set of System health checks changes (looked at every five minutes, only while a
webhook takes them, the first look only learns). Health events belong to no library, so the library filter does not hold them back.
An adult library's events reach only a webhook with *include adult libraries*; file paths are in the payload only with
*include file paths*.

Each event goes into an outbox; a task sends due rows every minute (10 s timeout, the receiver's host only; a redirect is never followed, to the same host or another). A 2xx removes the row; any other answer waits 1 minute, 5 minutes, 30 minutes, 2, 6, 12 and 24 hours; the eighth
failure drops the row. A 410 switches the webhook off. The job is refused in a read-only or observing instance.

Headers: `X-Solarr-Event`, `X-Solarr-Delivery` (the outbox row id) and, with a secret, `X-Solarr-Signature: sha256=<hex>`, the
HMAC-SHA256 of the body. Verify in shell:

```sh
printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | sed 's/^.* /sha256=/'
```

Payload `schema: 1`: `schema`, `event`, `at`, `delivery`, `instance{name, version}`, `library{id, name, media_type, kind}`,
`work{id, title, year, ids}`, `parts`, `release`, `files`, `previous_files`, `message`. Fields are only ever added.

Presets (Settings > Providers, Webhook presets): `generic` (the payload), `discord`, `slack`, `ntfy`, `gotify`, `apprise`. A
preset is a method, headers, a content type and a body template; `json` quotes a value so a title with quotes cannot break
the body.

API: `/api/v1/webhooks` (list, add), `/webhooks/{id}` (replace, delete), `/webhooks/{id}/test`, `/webhooks/events`.

## Incoming

Connections > Webhooks > Incoming makes a token for another tool. The token (32 random bytes, base64url) is shown once; only its
SHA-256 is stored and it is compared in constant time. Rotate it (`New token`) to cut off the old one.

`POST /api/v1/hooks/{id}` with `Authorization: Bearer <token>` or `X-Solarr-Token: <token>`; `?token=` works for a sender that
cannot set a header (request paths are logged without the query). An unknown id, a wrong token and a disabled hook all answer
the same 404. Sixty calls a minute per hook (429 with `Retry-After`), bodies up to 64 KiB. The body is a JSON object with
`action` (or `?action=`; implied when the token may do one thing) and the fields of the action; fields it does not know are
ignored, so a media server's own payload passes. An action the token was not given answers 403. Every action only enqueues the
job its button enqueues and answers `202 {"job_id": 12, "duplicate": false}` (`job_id` is 0 where the work's own module queues
the job, as for a refresh).

| Action | Body | Does | New token |
|---|---|---|---|
| `scan` | `library_id`, or `path` inside one of the token's library folders | the library or folder scan | on |
| `import` | `path` of a folder inside a download folder (never the download folder itself) | the import of that folder, as a watched folder is | on |
| `refresh` | `work_id`, or `provider` and `id` | the work's metadata refresh | on |
| `playback` | anything | reads the watch state of the token's media server now | on |
| `search` | `work_id` or `library_id` | starts the hunt | off, with a warning |

Paths are resolved (symlinks included) and must lie inside the allowed folder. Import refuses a mode that leaves libraries
alone; search refuses a mode that does not search. Admin API: `/api/v1/incoming-webhooks` (list, add, replace, delete) and
`/incoming-webhooks/{id}/token` (rotate).
