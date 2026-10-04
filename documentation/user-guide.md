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
  **qBittorrent** (torrents) is added the same way, with a user name and password (empty when it lets Solarr's
  address in); Lidarr's qBittorrent can be taken from its Connections page, its user name filled in for you. A torrent is followed by its info hash; it is not removed after import unless you tick that
  (it keeps seeding). With that ticked, **Seed to ratio** and **Seed for (minutes)** keep an imported torrent in qBittorrent until it has shared that much or seeded that long, whichever comes first, and only then remove it with its files; neither set removes it at once. A torrent you removed in qBittorrent yourself is simply considered done. If qBittorrent names its download folder differently from Solarr, set the path
  mapping on the client. A client serves the protocol it is added with, so a torrent release needs a
  torrent client.

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
**Fresh** means released within a week, or shown by an RSS feed within 48 hours whatever the release date
(a manual search never counts), and those albums are also searched first.
Select all matching now respects the Fresh filter, for Monitor and Unmonitor too. **Library → Duplicates** (advanced) lists tracks held more than once, found by artist, title and length (a flac and a 96 kbit mp3 of one song, on any albums), and recommends the copy closer to the library's target quality. You choose the copy to keep (a radio per copy) and, for each other copy, whether its paths become links to the kept file (hardlink or reflink; the other album keeps its track, the old file waits seven days in the recycle bin; only between files of the same extension) or the copy goes to the recycle bin; a confirmation lists every path before anything changes. Above it, **Identical files** covers the other case, the very same bytes stored at two paths: a preview of which second paths can become a reflink (else a hardlink) of the first, per pair on one filesystem, and a **Link identical files** button (manage access) that compares every byte again before swapping a path atomically. Roots that are offline, not writable or read-only are never touched. **Settings → Tags** lists
the tags you made, with how many albums carry each, and renames or deletes them (administrator).

With an empty search box, Discover lists **artists you have albums of, but not all**, from your own
library compared with each artist's discography (live albums and compilations left out): the most
missing first, with one click to follow the artist and add the rest. It fills in after the weekly
comparison has run (Settings → Scheduler → Compare discographies).

On an album's or artist's page, **Choose image** lists the pictures the metadata providers hold
(Cover Art Archive, TheAudioDB, Wikidata); the one you pick is kept over later refreshes.

## 4b. Add TV

Create a library of media type **TV** (Settings → Libraries), give it a root folder, and a scan reads your folders:
each series folder is matched by name, `{tvdb-123}` in the folder name, or what you already added, and every episode file
is attached to its episode. **Settings → Libraries → Edit** has **Monitor series found by a scan**: only the episodes on
disk (the default, so nothing is hunted that you did not ask for), every episode, or none.

**Add series** searches TheTVDB (through Sonarr's free proxy) and, with a key, TMDB; choose what to monitor (Sonarr's nine
options), the quality profile, and whether to search for missing episodes at once. Free metadata is enough to start, but
**Settings → Metadata → TV series** takes your own **TMDB key** (a free account at themoviedb.org: paste the API Read Access
Token or the API key) and then adds cast, crew and guest stars, networks with logos, artwork and alternative titles; **Test
the providers** checks all five sources. Anime also gets its romaji and synonym titles from AniList, which release groups
use.

Under a TV library: **Series** (as posters with a progress bar, as an overview with the plot, or as a sortable table; filters by status, network, genre and quality), **People**
(everyone credited, by role; open one for their credits here, their biography and what else they are known for),
**Networks**, **Wanted**
(aired episodes without a file) and **Unidentified**. A series page lists its seasons and episodes with directors,
writers and guest stars; **Edit** re-applies any monitor choice to what you already hold and gives the series its own
quality profile. Wanting one episode also wants its season.

**Edit → Delete series** removes a series; tick "move its files to the recycle bin" to recycle them, and keep "do not add it back"
ticked so a scan, a follow or an import list leaves it out (the Series index lists the deleted ones, with **Allow again**).
**Edit → Series type** shows what was detected (anime, daily or standard) and lets you change it; anime seasons are also searched
by absolute episode number. Posters and photos are fetched once and served by Solarr itself.

A TV file a scan could not place waits under **Unidentified**: open it, search the series, give the season and episodes when
the file name does not say ("3", "2, 3" or "4-6"), and it is placed for good; ticking "also place the other files in this
folder" places each by its own name.

**Quality profiles** for TV are edited with a form (Quality profiles in the sidebar): rank resolution, source, codec, HDR and
audio best first, set the lowest and highest resolution accepted, name codecs never to accept, require an audio language, and
list **preferred release groups** that rank above an otherwise equal release.

A TV file a scan could not place is asked about again every night (task **Identify waiting TV files**) and on demand with
**Try TV files again** on the Review page. A folder named in another language, or a title TheTVDB lists twice, is settled by the
episode numbers of its files when they clearly fit one series. **Settings → Metadata → TV series** orders the providers: the
first one that holds a field (plot, genres, rating, artwork) supplies it; the default is open and fast first (Skyhook, TVmaze,
TMDB, TheTVDB, AniList). Wikidata is listed there too: it recognises the same person in TV and music.

A TV library's **Quality** page takes a **target** of picture quality (resolution, video codec, HDR, audio codec, file type; a part left
on "any" is free). Releases that match it are ranked first inside the profile's own limits, and each episode on the series page shows what
the probe measured of its file (resolution, codec, HDR, audio, size, subtitles) and whether it meets the target. Subtitles count from the
file's own tracks and from sidecar files beside it (`Show - S01E01.eng.srt`); a language profile can require or prefer a subtitle language.
**Edit several** on the Series index (manage access) switches to the table with a checkbox per series; the bar above applies a monitor
choice or a quality profile to every ticked series, or deletes them, after a confirmation that says what will happen.
**Wanted → Cutoff unmet** (TV) lists the episodes on disk whose measured file is below the library's target or cutoff, each with an
interactive search for that one episode; it needs a target or cutoff on the Quality page.
The TV **Statistics** page breaks the library down by resolution, codec, HDR and audio, shows size and bitrate per hour of picture,
the 20 biggest series and the 20 with most episodes missing. The probe reads video files in the background (a first pass over a large
library takes hours); the figures fill in as it goes.

**Follow** (on a person's or a network's page, with your TMDB key) adds their new series for you: a person by the roles you
tick (acting, directing, writing, ...), a network for everything new it airs. At most five series per follow are added each
time it runs, with the monitor choice you set; older series only if you ask for the back catalogue. The task **Refresh
series** (Settings → Scheduler) re-reads running series twice a day so new episodes and seasons arrive by themselves.
Before you follow, the panel lists the series it would add right now (and how many already are in your library), so nothing
arrives unseen.

**The same person in more than one library**: an actor who is also a musician in your music library is one person. Solarr
recognises them through their Wikidata item (never by name alone): the task **Match people across libraries** asks TMDB for
the item of TV people who share a name with a music artist, and a refreshed artist records theirs. Their TV page then shows
**Also in your other libraries** (their albums), with a link to the full profile.

**Import from Sonarr** (Connections → Arr): the series, the monitored state of every episode and the quality profile (by
name) of each series come over; import before the first scan, because a series already in Solarr is left as it is.

## 5. What happens on its own

Three scheduled tasks (Settings → Scheduler) find what is missing without you pressing anything:

- **RSS sync** (every 15 minutes) reads each indexer's newest releases and grabs one a wanted album
  accepts. An album an RSS feed first showed a release for counts as newly arrived for the **RSS freshness
  window** (Settings → Libraries → Priority, advanced; default 48 hours), as one released within the
  **Freshness window** (default 7 days) does.
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
  committed exactly like a folder. A folder you pointed at keeps its archives until everything in them
  is in the library, then they move into a `.solarr-recycle` folder inside it (deleted after a week by
  the next import of that folder); a read-only source is never touched. A download that is only an
  archive imports the same way.
- **Add unrecognised albums** (off by default): a folder no metadata source recognises still
  becomes an album, named by its tags or folder and marked **Unverified**. It is not searched for;
  commit it from the review with the right candidate and its files move onto the real album.
- A folder held for review has **Commit anyway** (its best match) and **Identify as…**: search for the
  album the files are, pick it, and they are placed as that album, which is also the way out for a folder
  nothing could identify. Both need the session to have a library.
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

**TV quality targets and profiles (run 10).** A quality profile's target for video now also takes file types (mkv, mp4, avi, wmv, mpeg, ts ...), audio channels, audio and subtitle languages, audio and video bitrate (kbit/s, within a tolerance) and frame rate; the same fields exist in the general profile editor as tiers and targets. TV People, Series and Networks use the same scrolling grid, tile sizes, view options and selection as music. A background task, **Fetching series pictures**, caches posters and photos ahead of time so the first scroll is as quick as music; the provider speeds behind the default order are in [tv-providers-benchmark.md](tv-providers-benchmark.md).

**Recycle bin** (Storage) lists what the bins hold, newest first, with the day each file is removed for good, and an administrator can **Empty recycle bin** by hand. **Language profiles** are made under Quality profiles, below Target qualities. A series' poster can be changed with **Choose image** beside it (the posters the providers hold; yours is kept over refreshes), and an **Also known for** title on a person's page opens Solarr's add panel (or the series, when the library already has it). **TV Duplicates** are two files on one episode record, never two episodes that merely share a title or a length.

**TV upgrades.** With a target or cutoff set on a series library, a season whose files all exist but fall short of it is hunted for a better release (Wanted > Cutoff unmet lists the episodes). A download replaces an episode file only when it is better on resolution, codec, HDR, channels, bitrate and the other target fields; an equal one is dropped, and the replaced file waits in the recycle bin. Turn **Upgrades allowed** off in the library's Quality settings to stop it.

