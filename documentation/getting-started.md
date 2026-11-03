# Getting started

> **Level: 🟢 Beginner.** About 15 minutes from nothing to your first automatic download. Every step is done in
> the web interface; the only command you type is the one that starts the container.

By the end you will have Solarr running, a library of your music, TV or movies in it, an indexer and a download
client connected, and one item on its way.

## What you need

| You need | Why |
|---|---|
| Docker (or any container runtime) | Solarr is one container, one static binary |
| A folder with your media, or an empty one | Solarr scans it; it does not need to be tidy |
| *Optional:* an indexer (Prowlarr, or any Newznab/Torznab URL) and a download client (SABnzbd or qBittorrent) | Needed to **get** new media. Without them Solarr still organises what you have |

Already run Lidarr, Sonarr or Radarr? Keep them running while you try Solarr: it only reads them when you ask
(step 3).

## 1. Start it

```sh
docker run -d --name solarr \
  -e PUID=1000 -e PGID=1000 -e TZ=Europe/Berlin \
  -v /srv/solarr:/config \
  -v /srv/media/music:/music \
  -v /srv/downloads/complete:/downloads:ro \
  -p 5454:5454 \
  ghcr.io/makearr/solarr:latest
```

Or as a compose file:

```yaml
services:
  solarr:
    image: ghcr.io/makearr/solarr:latest
    container_name: solarr
    environment:
      PUID: 1000
      PGID: 1000
      TZ: Europe/Berlin
    volumes:
      - /srv/solarr:/config
      - /srv/media/music:/music
      - /srv/downloads/complete:/downloads:ro
    ports:
      - "5454:5454"
    restart: unless-stopped
```

| Line | Meaning |
|---|---|
| `PUID` / `PGID` | Who Solarr runs as, and who owns what it writes. Use the user that owns your media. It never changes the owner of files already there |
| `TZ` | Your time zone, so schedules and the calendar read right |
| `/config` | Settings and databases. **Back this up; keep it on a local disk, not a network share** |
| `/music` | Your media. Mount as many folders as you have libraries (`/series`, `/movies`, ...) |
| `/downloads:ro` | Your download client's *completed* folder, read-only. If the client names that folder differently, set a path mapping on the client (step 4) |

Open `http://<your-host>:5454`.

## 2. The setup wizard

The wizard asks for two things:

1. **An administrator**: a user name and password.
2. **Your first library**: a name (say "Music"), a type (music, TV, movies...) and the root folder it lives in
   (`/music`).

Solarr scans the folder straight away. Files whose tags carry MusicBrainz ids are recognised at once, the rest by
their track lists and names. Covers, posters and photos arrive over the next few minutes; you can already browse.

A root folder keeps **8 GB free** by default: automatic downloads stop before they would eat into it.

## 3. Starting from Lidarr, Sonarr or Radarr? (optional)

In the wizard, or later under **Connections → Arr**, connect each one in turn:

1. Enter its address and API key.
2. Press **Preview**. This reads the *Arr and changes nothing. It shows what is new, what is already here, and
   which Solarr quality profile each of the *Arr's profiles would become.
3. Press **Import**.

Details and options: [arr-import.md](arr-import.md).

## 4. Connect an indexer and a download client

The dashboard lists what is still missing until you do this.

1. **Connections → Indexers → Add**: choose Newznab or Torznab. With Prowlarr, use the per-indexer URL
   (`http://prowlarr:9696/<id>`) and Prowlarr's API key.
2. **Connections → Download clients → Add**: SABnzbd with its API key, or qBittorrent with user name and
   password. Press **Test**.

> **One rule matters:** give Solarr its **own category** in the client (the default, `solarr-music`, is fine).
> Never reuse the category Lidarr, Sonarr or Radarr download into: they would import Solarr's downloads. The test
> fails if it finds a clash.

These screens need the **advanced** interface level: **Settings → User → Interface level**. The levels are
explained in [the guide hub](user-guide.md#interface-levels).

## 5. Add something

**Library → Music → Add new** (or **Discover**): search for an artist or album and press add.

- **Monitor what I add**: let Solarr go and get it. Off keeps it in the library without searching.
- **Start search for missing albums**: search at once. On by default.

TV and movies work the same way under their own library. See [guide/music.md](guide/music.md) and
[guide/tv.md](guide/tv.md).

## 6. Watch it work

| Where | What you see |
|---|---|
| **Activity → Queue** | What is downloading, and what is held waiting for room |
| **Activity → History** | What was grabbed, imported or failed, and why |
| A work's own page → **Search** | When it was last searched, when it will be again, or **why it will not be** |
| The bell | What needs you: warnings and the week's failures |

When the download finishes, Solarr copies it into the library under your naming rules, checks the copy, and only
then cleans up. Nothing is deleted directly: replaced files wait in a recycle bin.

On posters and artist pages: 🟩 **green** complete · 🟥 **red** missing · 🟪 **purple** downloading · 🟨 **yellow**
not released yet · ⬜ **grey** unverified.

## 7. Let it run

You are done. On its own, every few minutes, Solarr reads your indexers' newest releases, searches the most
urgent missing items within an hourly limit, and follows each download to the end. A download that fails is
blocklisted and the item searched again. How and when: [guide/automation.md](guide/automation.md).

## If something looks wrong

| Symptom | Look at |
|---|---|
| Container does not start, or a permission error | `PUID`/`PGID` against the folder owner: [container-runtime.md](container-runtime.md) |
| A folder shows **offline** | The disk is unmounted or unreadable; Solarr rechecks every five minutes ([guide/storage.md](guide/storage.md)) |
| Nothing is found for an item | Its page → **Search** says why. Then **Interactive search** lists every release and every failing indexer |
| A download finishes but is not imported | The client names its download folder differently from Solarr: set the **path mapping** on the client ([guide/connections.md](guide/connections.md)) |
| Files a scan could not place | **Unidentified** under the library; see [guide/importing.md](guide/importing.md) |

## Where next

| If you want to... | Read |
|---|---|
| Understand every option | [User guide](user-guide.md) |
| Tune quality, naming, folders | [library-quality.md](library-quality.md), [naming.md](naming.md), [file-management.md](file-management.md) |
| Set everything from compose | [environment-variables.md](environment-variables.md) |
| Import a messy folder | [guide/importing.md](guide/importing.md) |
| Hack on Solarr | [codebase-map.md](codebase-map.md), [coding-standards.md](coding-standards.md) |
