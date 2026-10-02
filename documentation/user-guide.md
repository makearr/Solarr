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

**Edit** (the pencil on an album's poster or row, as in Radarr) sets monitored, the album's **own quality
profile** (used instead of the library's profiles when it is searched or upgraded; the library's cutoff
still decides whether it counts as upgradable) and your **tags** (new ones are created as you type).
Tags a metadata provider assigned are never touched. With albums selected (Table view, or Select all
matching), **Edit** in the toolbar applies only what you change to all of them, after showing the count.
Select all matching now respects the Fresh filter, for Monitor and Unmonitor too. **Settings → Tags** lists
the tags you made, with how many albums carry each, and renames or deletes them (administrator).

With an empty search box, Discover lists **artists you have albums of, but not all**, from your own
library compared with each artist's discography (live albums and compilations left out): the most
missing first, with one click to follow the artist and add the rest. It fills in after the weekly
comparison has run (Settings → Scheduler → Compare discographies).

On an album's or artist's page, **Choose image** lists the pictures the metadata providers hold
(Cover Art Archive, TheAudioDB, Wikidata); the one you pick is kept over later refreshes.

## 5. What happens on its own

Three scheduled tasks (Settings → Scheduler) find what is missing without you pressing anything:

- **RSS sync** (every 15 minutes) reads each indexer's newest releases and grabs one a wanted album
  accepts.
- **Search for missing albums** (every 15 minutes) searches the most urgent wanted albums, within
  **Automatic searches per hour** (Settings → Libraries → Priority, advanced; default 12, 0 turns it off). Each
  hour's searches are split 40% new releases, 40% older gaps and 20% **upgrades** (albums below the
  library's cutoff); a share one group leaves unused goes to the others. **Automatic search order**
  (same section) picks the order within a share: oldest or newest searched, oldest or newest release,
  or a random pick among the oldest searched. An album just searched waits 6 hours, then 12, 24,
  doubling to a week; after a failed download it is tried again within a day. An upgrade also waits
  until the file it would replace is a day old, and is left alone once it has had three imports in
  a week.
- **Check downloads** (every minute) follows each download to imported and cleaned up. A download
  that fails is blocklisted and the album searched again.

Your own grabs always go to the download client; automatic ones wait ("held") when its queue is
full, and stop when the library's root folders have no space above their reserve.

## 6. Follow what is going on

- **Activity → Queue**: what is downloading, and what is held waiting for room.
- **Activity → History**: what was grabbed, imported or failed, and why.
- **Library → Wanted** lists what is **Missing**, and under **Cutoff Unmet** the complete albums whose
  files are below the library's quality. **Search all** there looks for a strictly better release of
  each; the automatic search does the same a few at a time.
- **Activity → Notifications** (and the bell in the header) collects what needs you: the warnings the
  dashboard shows, and the last week's failed downloads and imports.
- **Library → Statistics**: monitored albums by state (each figure opens those albums), files and bytes
  by audio codec, growth by month (when Solarr first saw each file), the last 30 days' release decisions
  and downloads. Computed once a day, not live.
- **Calendar**: month, week, a seven-day forecast, one day, an agenda and a year view of the releases of
  artists you follow and albums you have asked for. Green has it, blue is wanted and not out yet, red
  wanted and out.
- An **album's page**, under Search, says when it was last searched and when it will be searched
  again -- or why it will not: a download on its way, no indexer, searching switched off, no space,
  or waiting for its release date. **Interactive search** lists every release found, best first,
  and names any indexer that failed or did not answer in time.

## 7. Import a folder (advanced, administrators)

**Storage → Import**: point at any folder -- one album, a whole collection, a messy download
folder. Solarr works out what is in it.

- **Preview first** places nothing until you commit from the review below.
- **Only into library** limits the import to one library; **Add as a root instead of moving** makes the
  folder itself a root of that library, scanned where it is, instead of moving files out of it.
- **Archives are opened like folders**: a zip, rar or 7z (multipart sets as one, archives inside
  archives too) is opened into a workspace under the cache folder and its contents are inspected and
  committed exactly like a folder. The archive itself is never changed. A download that is only an
  archive imports the same way.
- **Add unrecognised albums** (off by default): a folder no metadata source recognises still
  becomes an album, named by its tags or folder and marked **Unverified**. It is not searched for;
  commit it from the review with the right candidate and its files move onto the real album.
- Every import can be **reverted** from its session.

Files a library scan could not identify wait under **Library → Music → Unidentified**.

## 8. Libraries and storage

- **Storage → Root folders** groups the roots under their library; a root folder belongs to at most one.
  Each card carries the policy (read only, allow rename, reserve), capacity, and **Preview rename / drain**
  to move files off a disk.
- **Library → Music → File management** (advanced): **Merge Artists Stored Under Several Names** folds
  duplicate artists into one after each disk scan; **Navidrome profile** (under naming → tags) also fills
  the track artist's tags and puts a `cover.jpg` in each album folder that has none.
- **Library → Music → Quality** and **Quality profiles**: a profile ranks codecs, file extensions and
  containers (an `.m4a` and an `.mp4` are both MP4), channels and bitrate targets.
- The Albums view sorts by title, artist, release, added, size on disk, quality profile, status, genres or
  tags, and shows posters, an overview or a table.

## 9. What the colours mean

On posters and artist pages: **green** complete, **red** missing, **purple** downloading,
**yellow** not released yet, **grey** unverified.
