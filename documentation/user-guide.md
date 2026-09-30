# User guide

For people running Solarr, not developing it. It covers music, the one media type built so far,
and doubles as the acceptance walk-through for milestone M1 (`private/revision.md` §6.0): every
step below can be done from the web interface.

Some screens only appear at a higher **interface level** (Settings → User): **normal** shows what
running a library needs, **advanced** adds connections, import, storage and tuning, **expert** the
rest. Steps below say when a level is needed.

## 1. Install

One container. `/config` holds the configuration and databases; mount your music where you keep
it, and your download client's completed folder read-only.

```sh
docker run -d --name solarr \
  -e PUID=1000 -e PGID=1000 -e TZ=Europe/Berlin \
  -v /srv/solarr:/config \
  -v /srv/music:/music \
  -v /srv/downloads/complete:/downloads:ro \
  -p 5454:5454 \
  ghcr.io/makearr/solarr:latest
```

`PUID`/`PGID` decide who Solarr runs as and who owns what it writes; it never changes the owner
of your music (see [container-runtime.md](container-runtime.md)). Open `http://<host>:5454`.

## 2. First run

The setup wizard creates the first administrator and your first library: a name ("Music") and
the root folder it lives in (`/music`). Attaching a root folder scans it: albums whose files carry
MusicBrainz tags are recognised at once, others by their tracklists and names. Covers and artist
pictures arrive over the following minutes.

A root folder keeps a **reserve** of free space (8 GB by default, Storage → Root folders,
advanced): automatic downloads stop before they would eat into it.

## 3. Connect an indexer and a download client (advanced)

- **Connections → Indexers → Add**: a Newznab or Torznab endpoint. With Prowlarr, use its per-indexer
  URL (`http://prowlarr:9696/<id>`) and Prowlarr's API key. **Music categories** (default 3000, Audio)
  are what every search and RSS read asks for; narrow them (3040 is lossless) if you only want some.
- **Connections → Download clients → Add**: SABnzbd, with its API key. Solarr uses its own category
  in the client and only ever touches downloads in it.

The dashboard lists what is missing (no indexer, no client, a root folder out of space) until it is
fixed.

## 4. Add music

**Library → Music → Add new** (or Discover): search for an album or an artist.

- **Monitor what I add**: whether Solarr should get it. Off keeps it in the library without
  looking for it.
- **Start search for missing albums** (on by default, remembered for you): an album is searched at
  once; an artist's albums are added straight away and their missing ones searched.

Monitoring an album later from its own page also searches it at once while that box is ticked.

## 5. What happens on its own

Three scheduled tasks (Settings → Scheduler) find what is missing without you pressing anything:

- **RSS sync** (every 15 minutes) reads each indexer's newest releases and grabs one a wanted album
  accepts.
- **Search for missing albums** (every 15 minutes) searches the most urgent missing albums, within
  **Automatic searches per hour** (Settings → Priority, advanced; default 12, 0 turns it off). An
  album just searched waits 6 hours, then 12, 24, doubling to a week.
- **Check downloads** (every minute) follows each download to imported and cleaned up. A download
  that fails is blocklisted and the album searched again.

Your own grabs always go to the download client; automatic ones wait ("held") when its queue is
full, and stop when the library's root folders have no space above their reserve.

## 6. Follow what is going on

- **Activity → Queue**: what is downloading, and what is held waiting for room.
- **Activity → History**: what was grabbed, imported or failed, and why.
- An **album's page**, under Search, says when it was last searched and when it will be searched
  again -- or why it will not: a download on its way, no indexer, searching switched off, no space,
  or waiting for its release date. **Interactive search** lists every release found, best first,
  and names any indexer that failed or did not answer in time.

## 7. Import a folder (advanced, administrators)

**Storage → Import**: point at any folder -- one album, a whole collection, a messy download
folder. Solarr works out what is in it.

- **Preview first** places nothing until you commit from the review below.
- **Add unrecognised albums** (on by default): a folder no metadata source recognises still
  becomes an album, named by its tags or folder and marked **Unverified**. It is not searched for;
  commit it from the review with the right candidate and its files move onto the real album.
- Every import can be **reverted** from its session.

Files a library scan could not identify wait under **Library → Music → Unidentified**.

## 8. What the colours mean

On posters and artist pages: **green** complete, **red** missing, **purple** downloading,
**yellow** not released yet, **grey** unverified.
