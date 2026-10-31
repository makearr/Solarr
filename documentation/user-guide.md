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
the root folder it lives in (`/music`). Already running Lidarr, Sonarr or Radarr? Connect each one there in turn:
its root folders become a library, and you choose what its import brings in (monitored and unmonitored items,
which albums, how its paths read here). **Preview** reads the instance and changes nothing: per root folder where
it is here, its library, and how many items are new, already in Solarr or without an id; per quality profile of the
*Arr the Solarr profile its items take (the one of the same name is proposed, else the library's own; choose another
there); and the *Arr's Media Management settings that differ from the library's, taken over when ticked. **Import**
then runs with that mapping. Attaching a root folder scans it: albums whose files carry
MusicBrainz tags are recognised at once, others by their tracklists and names. Covers and artist
pictures arrive over the following minutes.

A root folder keeps a **reserve** of free space (8 GB by default, Storage → Folders,
advanced): automatic downloads stop before they would eat into it.

## 3. Connect an indexer and a download client (advanced)

- **Connections → Indexers → Add**: a Newznab or Torznab endpoint. With Prowlarr, use its per-indexer
  URL (`http://prowlarr:9696/<id>`) and Prowlarr's API key. **Music categories** (default 3000, Audio)
  are what every search and RSS read asks for; narrow them (3040 is lossless) if you only want some.
- **Connections → Download clients → Add**: SABnzbd, with its API key. Solarr uses its own category
  in the client (`solarr-music`, `solarr-tv`, `solarr-movie` unless you name one) and only ever touches downloads in
  it. Never give it a category Lidarr, Sonarr or Radarr download into: they would import Solarr's downloads. The
  connection test fails when a connected *Arr uses the same category on the same client.
  **qBittorrent** (torrents) is added the same way, with a user name and password (empty when it lets Solarr's
  address in); Lidarr's qBittorrent can be taken from its Connections page, its user name filled in for you. A torrent is followed by its info hash; it is not removed after import unless you tick that
  (it keeps seeding). With that ticked, **Seed to ratio** and **Seed for (minutes)** keep an imported torrent in qBittorrent until it has shared that much or seeded that long, whichever comes first, and only then remove it with its files; neither set removes it at once. A torrent you removed in qBittorrent yourself is simply considered done. If qBittorrent names its download folder differently from Solarr, set the path
  mapping on the client. A client serves the protocol it is added with, so a torrent release needs a
  torrent client.

The dashboard lists what is missing (no indexer, no client, a root folder out of space) until it is
fixed.

## 4. Add music

**Library → Music → Add new** (or Discover): search for an album or an artist.

**Library → Music → Tracks** lists every track, album by album; its search box finds a track by its title, album or
artist, and **Filter** narrows by title, album, artist, year, monitored or whether it has a file.

- **Monitor what I add**: whether Solarr should get it. Off keeps it in the library without
  looking for it.
- **Start search for missing albums** (on by default, remembered for you): an album is searched at
  once; an artist's albums are added straight away and their missing ones searched.

Monitoring an album later from its own page also searches it at once while that box is ticked.

**Edit** (the pencil on an album's poster or row, as in Radarr) sets monitored, the album's **own quality
profile** (used instead of the library's profiles when it is searched or upgraded; the library's cutoff
still decides whether it counts as upgradable) and your **tags**: typing suggests the library's genres (Jazz,
Science Fiction) and your own labels, and a name no tag has becomes a new label. In a library
with several root folders it also sets the **root folder**: choosing another one shows how many files move and
moves them on Save (all or none, through the same copy-verify-delete as every move); later downloads follow.
Tags a metadata provider assigned are never touched. The **Tags** button beside the title filter narrows any library
to works carrying a tag, a genre including everything below it (Rock finds Metal); a click on a chip turns it into
"not", and several chips must all hold. With albums selected (Table view, or Select all
matching), **Edit** in the toolbar applies only what you change to all of them, after showing the count.
**Fresh** means released within a week, or shown by an RSS feed within 48 hours whatever the release date
(a manual search never counts), and those albums are also searched first.
Select all matching now respects the Fresh filter, for Monitor and Unmonitor too. **Library → Duplicates** (advanced) lists tracks held more than once, found by artist, title and length (a flac and a 96 kbit mp3 of one song, on any albums), and recommends the copy closer to the library's target quality. You choose the copy to keep (a radio per copy) and, for each other copy, whether its paths become links to the kept file (hardlink or reflink; the other album keeps its track, the old file waits seven days in the recycle bin; only between files of the same extension) or the copy goes to the recycle bin; a confirmation lists every path before anything changes. Above it, **Identical files** covers the other case, the very same bytes stored at two paths: a preview of which second paths can become a reflink (else a hardlink) of the first, per pair on one filesystem, and a **Link identical files** button (manage access) that compares every byte again before swapping a path atomically. Roots that are offline, not writable or read-only are never touched. **Settings → Tags** shows
every genre as a tree, one per kind of media (Music; Film and TV), and your free labels, with how many items carry
each; an administrator renames, moves a tag under another, merges two (the merged name keeps leading there when a
provider uses it again), adds or deletes them.

**Maximum rating** (Settings → Libraries → Edit, TV and movie libraries): a Kids library set to "13 (PG-13)" keeps
anything rated higher, or unrated unless you allow it, where it is but never searches for it there; its list's
**Above rating** button shows those, their pages say so, and a notice counts them.

**Move to library** (a work's page, or a selection in the list; administrator) moves or copies works to another library
of the same kind, a series to Anime say: preview shows every file's new place under that library's naming, with the
subtitles and pictures that come along; a copy is unmonitored in the other library and shares the file's bytes where
the disk allows.

**A library of several kinds** (Settings → Libraries → **Also holds…**): an Anime library can hold movies as well as
series. Each kind has its own root folders, quality, naming and monitoring (Edit on its "also holds" row); name,
priority, maximum rating and who may see it are the library's. The library is one entry in the tree: **All** lists
every kind with a Series/Movies chip, each kind's own pages follow (Cast · Movies where a name repeats), Add switches
between the kinds, and Wanted, Insights, Files and Settings have a tab per kind.

**Which library?** Adding from the dashboard picks the library the libraries' rules suggest (an anime goes to Anime:
"Animation, original language ja"), always shown with its reason and changeable; a library's own Add says when another
fits better. A library's kind brings its rules (Anime, Documentary, Kids, Reality); its settings' **Suggested for**
tab (advanced) changes them. **Files → Belongs elsewhere** lists what a library holds that belongs in another, to move
there, and says when a kind of library is missing ("No Documentary library: 4 here would go there").

With an empty search box, Discover lists **artists you have albums of, but not all**, from your own
library compared with each artist's discography (live albums and compilations left out): the most
missing first, with one click to follow the artist and add the rest. It fills in after the weekly
comparison has run (Settings → Scheduler → Compare discographies).

On an album's, series' or artist's page, **Choose image** lists the pictures the metadata providers hold
(Cover Art Archive, TheAudioDB, Wikidata; a series' posters from its TV sources); the one you pick is kept over later refreshes.

## 4b. Add TV

Create a library of media type **TV** (Settings → Libraries) and pick its **kind**: Series, Anime, Documentary, Reality or
Kids. A kind only sets the starting point: its quality profiles (Anime 1080p first for anime, a smaller x265 profile
for reality), its languages (English or German; Japanese, English or German for anime), and for anime that a series the
providers call standard is anime there, searched by absolute episode number and in the indexers' anime category too.
Give the library a root folder, and a scan reads your folders:
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
writers and guest stars; **Edit** re-applies any monitor choice to what you already hold, gives the series its own
quality profile and sets its type (standard, daily, anime). Wanting one episode also wants its season.

**Edit → Delete series** removes a series; tick "move its files to the recycle bin" to recycle them, and keep "do not add it back"
ticked so a scan, a follow or an import list leaves it out (the Series index lists the deleted ones, with **Allow again**).
An episode counts as missing a while after it airs (2 hours after an exact time, 12 after a likely one, 36 after a date
alone; **Settings → Metadata → Air times**, advanced), and a season nothing of which is out yet says **Not out yet** with
its first date. If a series' air times are off because its network's time zone is wrong, set **Edit → Air time zone**.
A series TheTVDB also lists in DVD, absolute or another order has **Numbering** on its page: pick the order, preview which
episodes move, apply; the files follow their episodes and keep their names until you rename them.
**Crossovers** (under a TV library) lists events across series and suggests episodes that look like one broadcast listed
under two series. On a series page, an episode's link button adds it to an event (shown as "Event, 2 of 4" in every
series it touches, with a page that monitors the whole event) or records it as the same broadcast as another series'
episode, so one file holds both; that file is then linked into the other series' folder too (a second copy or a
symbolic link when the two are on different disks, set under **File management → Shared episodes across disks**).
**Edit → Series type** shows what was detected (anime, daily or standard) and lets you change it; anime seasons are also searched
by absolute episode number. Posters and photos are fetched once and served by Solarr itself.

A TV file a scan could not place waits under **Unidentified**: open it, search the series, give the season and episodes when
the file name does not say ("3", "2, 3" or "4-6"), and it is placed for good; ticking "also place the other files in this
folder" places each by its own name.

**Quality profiles** for TV are edited with a form (Settings, then Quality profiles): rank resolution, source, codec, HDR and
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
the 20 biggest series and the 20 with most episodes missing (a music library's Statistics also lists its 20 biggest
albums). "Quality against the target" counts the files that miss the library's cutoff; the cutoff check refreshes it. The probe reads video files in the background (a first pass over a large
library takes hours); the figures fill in as it goes.

**Follow** (on a person's or a network's page) adds their new works to a library for you, one follow per library: an
artist's albums of the release types you tick, an actor's or director's new series by the roles you tick (with your
TMDB key), a network's new series. A TV follow adds at most five series each time it runs, with the monitor choice you
set; older works come only if you ask for the back catalogue. The task **Refresh
works** (Settings → Scheduler) re-reads running series twice a day so new episodes and seasons arrive by themselves, and
fills in albums' tracklists; **Refresh people** and **Fetch pictures** do the same for people and for covers and posters,
one task each for every kind of library.
Before you follow, the panel lists the series it would add right now (and how many already are in your library), so nothing
arrives unseen.

**The same person in more than one library**: an actor who is also a musician in your music library is one person. Solarr
recognises them through their Wikidata item (never by name alone): the task **Match people across libraries** asks TMDB for
the item of TV people who share a name with a music artist, and a refreshed artist records theirs. Their TV page then shows
**Also in your other libraries** (their albums), with a link to the full profile.

**Import from Sonarr** (Connections → Arr): the series, the monitored state of every episode and the quality profile of
each series (as mapped in the preview) come over, and Sonarr's Media Management (Episode Title Required, file date,
propers, empty folders...) if ticked; import before the first scan, because a series already in Solarr is left as it is.

### Import lists

A TV library's **Settings → Import lists** adds series from lists, as Sonarr's Import Lists do: an AniList season (the
current one, the next one, or a named one), an AniList user's list (by status), a public TMDB list, or TMDB's trending
or popular shows. TMDB's need your TMDB key under Settings → Metadata; AniList needs none. **Add list** asks for the
list's own settings, how often it syncs, whether and how what it adds is monitored, the quality profile, and titles or
ids to leave out. Saving opens the **preview**: every entry of the list and what enabling would do with it (added,
already in the library, removed by you, left out, not a series, not found). Nothing is added until you **Enable this
list**; it then runs at once and again on its schedule, and **Run now** runs it whenever you like. A series a list
added is never added again by it, even after you remove it. Changing a list's settings asks for a new preview.

## 5. What happens on its own

Three scheduled tasks (Settings → Scheduler) find what is missing without you pressing anything:

- **RSS sync** (every 15 minutes) reads each indexer's newest releases and grabs one a wanted album
  accepts. An album an RSS feed first showed a release for counts as newly arrived for the **RSS freshness
  window** (Settings → Libraries → Priority, advanced; default 48 hours), as one released within the
  **Freshness window** (default 7 days) does.
- **Search for what is missing** searches the most urgent wanted items, within **Automatic searches per hour**
  (Settings → Libraries → Automatic searching, advanced; default 30, 0 turns it off), spread over its runs: **Search
  every** (default 15 minutes) sets how often one starts. Each run's searches are split 40% new releases, 40% older
  gaps and 20% **upgrades** (items below the library's cutoff); a share one group leaves unused goes to the others.
  Each library has a **share** of the searches (default 1; 2 gets twice as many, 0 none), and what one library
  cannot use goes to the others. **Automatic search order**
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
- **Every work has one page** (an album, a series), with the same action row -- Monitor, Search, Refresh, Edit,
  Remove -- and tabs: **Overview** (a series' seasons as Sonarr shows them, an album's tracklist as Lidarr does),
  **Files** (what is on disk and what the probe measured), **History** and **Search**. Search says when the work was
  last searched and when it will be searched again -- or why it will not: a download on its way, no indexer,
  searching switched off, no space, or waiting for its release date. **Interactive search** lists every release
  found, best first, and names any indexer that failed or did not answer in time. **Remove** can also delete the
  files (to the recycle bin) and keep the work from being added back by a follow or an import list.

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
  album or series the files are, pick it, and they are placed as that, which is also the way out for a
  folder nothing could identify. Both need the session to have a library. In a TV library the episodes are
  read from the file names (`S01E02`, an air date, an absolute number); a series the library does not hold
  yet is added first, and a file for an episode the library already has is left where it is. **Commit anyway** on a
  held TV folder places it as its best guess only when that guess is at least 75% sure; below that, name the series
  with **Identify as…** (a folder named by hand is easily guessed wrong).
- **Import extra files** (each library's **File management → Importing**; on for TV, off for music) brings files with
  the listed extensions along:
  lyrics, cue sheets and logs beside an album, and in a TV library the subtitles named after an episode
  (`Show.S01E02.en.srt` becomes `Show - S01E02 - Title.en.srt`). Nothing already there is overwritten.
- Every import can be **reverted** from its session.

Files a library scan could not identify wait under **Library → Music → Unidentified**.

## 8. Libraries and storage

- **Storage → Folders** holds the root folders, grouped under their library (a root folder belongs to at most one), and
  the download folders. Each root's card carries the policy (read only, allow rename, reserve), capacity, **Preview
  rename** (it names the files even on a root it may not change, and says why) and **Drain…**, which shows what would
  move and asks before moving every file to the library's other roots. Removing a folder, or taking a root out of its
  library, asks first.
- **Settings → Cache** (expert) bounds the provider answers and resized artwork Solarr keeps: a size limit (10 GiB),
  an age (90 days), the images/metadata split, and **Keep disk free** (10%): when the disk under the cache runs short of
  that, the next cache maintenance shrinks the cache by the shortfall, below its size limit. The page shows the disk's
  size and free space and warns when the size limit is larger than the disk. Clearing it is always safe.
- **A root Solarr cannot reach** (missing, unreadable, or empty although it held files: an unmounted disk) is declared
  offline by Solarr, checked every five minutes, and declared online again once reached. A root *you* declare offline
  stays offline until you declare it online.
- **An album held twice** (the same release found under two albums, for example after MusicBrainz merged two release
  groups) shows a warning on its page naming the other one. **Merge into it…** shows what moves; **Merge** moves its files,
  wanted state, downloads and history onto the other album, keeps its name and old ids as the other's aliases, and removes it.
- **A file you delete** is noticed by the next **full** scan: its album or season is missing again and searched for, unless
  the library unmonitors deleted files. If a full scan finds a great many files of a root gone at once (more than 50 and
  more than one in twenty), it marks none of them and the bell says so: a disk that was not mounted is not a deleted
  library. Run a full scan once the disk is back, or press **Accept as gone** on the root's card.
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

**Recycle bin** (Storage, administrators) lists what the bins hold, newest first, with the day each file is removed for good, and an administrator can **Empty recycle bin** by hand. **Language profiles** are made under Quality profiles, below Target qualities. A series' poster can be changed with **Choose image** beside it (the posters the providers hold; yours is kept over refreshes), and an **Also known for** title on a person's page opens Solarr's add panel (or the series, when the library already has it); an actor who also records lists their MusicBrainz studio albums and EPs there too (found through Wikidata), added to the music library. **Collections** (a library's Collections level) holds the providers' collections and franchises beside your own; one a provider or Solarr made can be deleted, or a work taken out of it, and neither comes back on a refresh. **Monitor collection** on a provider's collection adds the works of it you lack; on one you made it monitors every work in it, and every work you put in it while it stays monitored. **TV Duplicates** are two files on one episode record, never two episodes that merely share a title or a length.

**Filter** (Series, Cast, and the Artists and Albums levels) is Sonarr's custom filter: build conditions (field, operator, value) that must all hold, optionally name and save them, and pick a saved one from the menu; the active filter is in the address, so a link keeps it. Artists, Cast and the **People** entry in the sidebar (everyone from every library you may read) are one list: Posters,
Overview and Table views, role chips, sorted by name, number of works, newest work or when added, either way. A person
has one page wherever you open them: who they are, then their works in each of your libraries, shown as that library
shows them (an artist's albums by release type, an actor's series as posters with the parts they played), what TMDB also
knows them for (with **Add to Solarr**), and **Follow** for each library they could be followed into. Networks and
labels work the same way: a music library's **Labels** level lists the labels of its albums (from MusicBrainz, filled
in by the hourly metadata run; their logos come from Wikidata, Settings → Scheduler → Find label logos), and following a label adds what it releases from then on, or its back catalogue, up
to 25 albums a run, with the release types you tick. People who are both an actor and a music artist are matched across libraries by their Wikidata item; a name that differs only in punctuation (a quoted nickname) is now tried too.

**TV upgrades.** With a target or cutoff set on a series library, a season whose files all exist but fall short of it is hunted for a better release (Wanted > Cutoff unmet lists the episodes). A download replaces an episode file only when it is better on resolution, codec, HDR, channels, bitrate and the other target fields; an equal one is dropped, and the replaced file waits in the recycle bin. Turn **Upgrades allowed** off in the library's Quality settings to stop it.

