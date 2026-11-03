# Connections: indexers and download clients

> **Level: 🟡 Advanced.** Needs the *advanced* interface level. First time? Do [getting started](../getting-started.md) step 4 first.

## Indexers (🟢 basics)

**Connections → Indexers → Add**: a Newznab or Torznab endpoint.

- With Prowlarr, use its per-indexer URL (`http://prowlarr:9696/<id>`) and Prowlarr's API key.
- **Music categories** (default 3000, Audio) are what every search and RSS read asks for. Narrow them (3040 is
  lossless) if you only want some.

## Download clients

**Connections → Download clients → Add**. A client serves the protocol it is added with, so a torrent release
needs a torrent client.

### The category rule (🟢 read this)

Solarr uses its own category in the client (`solarr-music`, `solarr-tv`, `solarr-movie` unless you name one) and
only ever touches downloads in it.

> Never give it a category Lidarr, Sonarr or Radarr download into: they would import Solarr's downloads. The
> connection test fails when a connected *Arr uses the same category on the same client.

### SABnzbd

Add it with its API key.

### qBittorrent (🟡)

Added with a user name and password (empty when it lets Solarr's address in). Lidarr's qBittorrent can be taken
from its Connections page, with the user name filled in for you.

- A torrent is followed by its info hash.
- It is **not removed after import** unless you tick that: it keeps seeding.
- With removal ticked, **Seed to ratio** and **Seed for (minutes)** keep an imported torrent in qBittorrent until
  it has shared that much or seeded that long, whichever comes first, and only then remove it with its files.
  Neither set removes it at once.
- A torrent you removed in qBittorrent yourself is simply considered done.

### Path mapping (🟡)

If the client names its download folder differently from Solarr, set the path mapping on the client.

## What the dashboard tells you

It lists what is missing (no indexer, no client, a root folder out of space) until it is fixed. For how
downloads are queued, held and ordered see [downloads.md](../downloads.md).
