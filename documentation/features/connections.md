# Media servers and webhooks

> **Level: 🟡 Advanced.** Solarr organises; your media server plays. They should talk.

## Feature set

**Media servers**
- **Jellyfin, Emby, Navidrome, Komga, Kavita, Audiobookshelf, Calibre and Stash** can be connected.
- **Told about new files** (a scan of the exact path where the server supports it), so new episodes show up at once.
- **Watch state** read back from servers that keep it, feeding a **Watched** list of works ready to clean up.
- **As a metadata source**, ranked after every online provider, only filling what they lack; their ids help recognition.
- **Path mapping** for servers that see your media under another path.
- **Plex:** no connection yet. Solarr's Plex naming preset lays files out the way Plex expects, and Plex finds them by
  its own scan.

**Webhooks**
- **Outgoing webhooks** with presets for generic JSON, Discord, Slack, ntfy, Gotify and Apprise: choose the events
  (grabbed, imported, upgraded, recycled, ...) and the libraries.
- **Incoming webhooks** with a token shown once (for example Jellyfin's webhook plugin).
- **Retries** with back-off through an outbox; a test button; a list of what is still waiting.

## Benefits

- **No more "why isn't it in Jellyfin yet?"**
- **Notifications where you already look**: your phone, your Discord, your chat.
- **Cleanup with confidence**: the Watched list shows what everyone has watched, previews the files and recycles them
  (restorable) instead of deleting.

## Technical details

- **Every outbound call** goes through the same guarded HTTP client as everything else (SSRF guard, optional proxy);
  credentials never follow a redirect to another host.
- **Webhook payloads** can be signed; the form shows the body of the chosen format for an example event.
- **Webhooks and provider definition files** travel in the configuration export, never an address, secret or token.

Further reading: [media servers](../media-servers.md), [webhooks](../webhooks.md).
