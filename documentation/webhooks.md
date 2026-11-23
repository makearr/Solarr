# Webhooks

## Outgoing

Settings: add a webhook with a name, a preset, the receiver's address, an optional secret, the events and the libraries (none
selected is every library). The address and the secret are sealed at rest and never returned; the list shows the address's host.

Events: `grabbed`, `imported`, `upgraded`, `import_failed`, `download_failed`, `renamed`, `file_recycled`, `work_added`,
`work_removed`, `request_created`, `request_approved`, `health_raised`, `health_cleared`, `test`. Built so far: `imported`,
`upgraded` and `file_recycled` (recorded where the media servers are told of a placed, changed or recycled file) and `test`.
An adult library's events reach only a webhook with *include adult libraries*; file paths are in the payload only with
*include file paths*.

Each event goes into an outbox; a task sends due rows every minute (10 s timeout, the receiver's host only, never a redirect to
another host). A 2xx removes the row; any other answer waits 1 minute, 5 minutes, 30 minutes, 2, 6, 12 and 24 hours; the eighth
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

Not built yet (token endpoints for scan, import, refresh, playback and search).
