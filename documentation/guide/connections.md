# Connections: indexers and download clients

> **Level: 🟡 Advanced.** Needs the *advanced* interface level. First time? Do [getting started](../getting-started.md) step 4 first.

## Indexers (🟢 basics)

**Connections → Indexers → Add**: a Newznab or Torznab endpoint.

- With Prowlarr, use its per-indexer URL (`http://prowlarr:9696/<id>`) and Prowlarr's API key.
- **Add indexers…** opens Prowlarr's catalog: pick from every indexer Solarr can search (private trackers, public
  trackers, Newznab presets) instead of typing a URL. A pasted Torznab URL that ends in `/api` is accepted.
  **Soulseek** is an indexer too, searched through slskd for every media type.
- **Music categories** (default 3000, Audio) are what every search and RSS read asks for. Narrow them (3040 is
  lossless) if you only want some.
- **Priority** breaks ties between equal releases and can be changed after adding. A paused indexer shows in Health.
- Every release is listed once in an interactive search, with what its title was recognised as (format, bitrate,
  resolution, source, codecs) as chips.
- Kinds and settings: [downloads.md](../downloads.md).

## Download clients

**Connections → Download clients → Add**. A client serves the protocol it is added with, so a torrent release
needs a torrent client.

### The category rule (🟢 read this)

Solarr offers every kind Lidarr and Sonarr do: SABnzbd, NZBGet, qBittorrent, Transmission, Vuze, Deluge, rTorrent,
µTorrent, Flood, aria2, slskd and the two blackholes. The **Test** button reports the folder the client says it
downloads into and offers it as a download folder. Solarr uses its own category in the client (`solarr-music`, `solarr-tv`, `solarr-movie` unless you name one) and
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
  Neither set removes it at once. **Wait for both** requires the ratio *and* the time, and **Hard stop (minutes)**
  removes it after that long whatever the ratio, for a private tracker that never lets the ratio be met.
- A torrent you removed in qBittorrent yourself is simply considered done.

### Path mapping (🟡)

Adding a client that reports a finished-downloads folder Solarr can read also watches that folder. If the client names its download folder differently from Solarr, set the path mapping on the client.

## What the dashboard tells you

It lists what is missing (no indexer, no client, a root folder out of space) until it is fixed. For how
downloads are queued, held and ordered (the held queue holds 200 releases by default, a setting on the Download clients page) see [downloads.md](../downloads.md).
