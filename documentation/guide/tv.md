# TV and anime

> **Level: 🟢 Beginner** for creating a library and adding series; 🟡 and 🔴 sections say what they need.
> Import lists have their own page: [import-lists.md](../import-lists.md).

## Create a TV library (🟢)

**Settings → Libraries**: create a library of media type **TV** and pick its **kind**: Series, Anime,
Documentary, Reality or Kids. A kind only sets the starting point:

- its quality profiles (1080p first for Anime, a smaller x265 profile for Reality);
- its languages (English or German; Japanese, English or German for Anime);
- for Anime, that a series the providers call standard is anime there, searched by absolute episode number and
  in the indexers' anime category too.

Give the library a root folder and a scan reads your folders: each series folder is matched by name,
`{tvdb-123}` in the folder name, or what you already added, and every episode file is attached to its episode.

**Settings → Libraries → Edit → Monitor series found by a scan** chooses what is hunted: only the episodes on
disk (the default, so nothing is hunted that you did not ask for), every episode, or none.

## Add a series (🟢)

**Add series** searches TheTVDB (through Sonarr's free proxy) and, with a key, TMDB. Choose what to monitor
(Sonarr's nine options), the quality profile, the root folder it goes under (with several roots; the first
file lands there), the series type, whether it uses season folders, tags, and whether to search for missing
episodes at once. Edit changes the type and the season folders later; **No season folders** puts episodes
straight in the series folder.

Free metadata is enough to start. For cast, crew and guest stars, networks with logos, artwork and alternative
titles, add your own **TMDB key** under **Settings → Metadata → TV series** (a free account at themoviedb.org:
paste the API Read Access Token or the API key). **Test the providers** checks all five sources. Anime also gets
its romaji and synonym titles from AniList, which release groups use.

## Find your way around (🟢)

Under a TV library:

| Page | What |
|---|---|
| **Series** | Posters with a progress bar, an overview with the plot, or a sortable table; filters by status, network, genre and quality |
| **People** | Everyone credited, by role. Open one for their credits here, biography and what else they are known for |
| **Networks** | Networks and their series |
| **Wanted** | Aired episodes without a file. **Cutoff unmet** lists episodes on disk below the target or cutoff (🟡, needs a target or cutoff on the Quality page), each with an interactive search |
| **Unidentified** | Files a scan could not place |
| **Statistics** | By resolution, codec, HDR and audio; size and bitrate per hour of picture; the 20 biggest series and the 20 with most episodes missing |

A series page lists its seasons and episodes with directors, writers and guest stars. **Edit** re-applies any
monitor choice to what you already hold, gives the series its own quality profile and sets its type (standard,
daily, anime). Wanting one episode also wants its season.

Posters and photos are fetched once and served by Solarr itself; the background task **Fetching series
pictures** caches them ahead of time.

## Unidentified files (🟢)

A TV file a scan could not place waits under **Unidentified**. Open it, search the series, give the season and
episodes when the file name does not say ("3", "2, 3" or "4-6"), and it is placed for good. Ticking "also place
the other files in this folder" places each by its own name.

Solarr asks again every night (task **Identify waiting TV files**) and on demand with **Try TV files again** on
the Review page. A folder named in another language, or a title TheTVDB lists twice, is settled by the episode
numbers of its files when they clearly fit one series.

## Removing a series (🟢)

**Edit → Delete series**. Tick "move its files to the recycle bin" to recycle them. Keep "do not add it back"
ticked so a scan, a follow or an import list leaves it out; the Series index lists the deleted ones, with
**Allow again**.

## Bulk edit (🟡)

**Edit several** on the Series index (manage access) switches to the table with a checkbox per series. The bar
above applies a monitor choice or a quality profile to every ticked series, or deletes them, after a
confirmation that says what will happen.

## Air times (🟡)

An episode counts as missing a while after it airs: 2 hours after an exact time, 12 after a likely one, 36 after
a date alone (**Settings → Metadata → Air times**). A season nothing of which is out yet says **Not out yet**
with its first date. If a series' air times are off because its network's time zone is wrong, set **Edit → Air
time zone**.

## Numbering and series type (🟡)

- A series TheTVDB also lists in DVD, absolute or another order has **Numbering** on its page: pick the order,
  preview which episodes move, apply. The files follow their episodes and keep their names until you rename them.
- **Edit → Series type** shows what was detected (anime, daily or standard) and lets you change it. Anime
  seasons are also searched by absolute episode number.

## Crossovers (🟡)

**Crossovers** (under a TV library) lists events across series and suggests episodes that look like one
broadcast listed under two series. On a series page, an episode's link button:

- adds it to an event (shown as "Event, 2 of 4" in every series it touches, with a page that monitors the whole
  event), or
- records it as the same broadcast as another series' episode, so one file holds both. That file is then linked
  into the other series' folder too (a second copy or a symbolic link when the two are on different disks, set
  under **File management → Shared episodes across disks**).

An event Solarr found itself stays under detection when you edit it: parts you take out are remembered by the
event's name and are not put back, parts you add stay, and the parts the providers list that the library lacks
remain. Renaming an event makes it yours. Adding a series to the library makes the next pass compare the
providers' episode lists again at once, so the parts it supplies show up.

## Quality for TV (🟡)

**Quality profiles** (Settings, then Quality profiles) are edited with a form: rank resolution, source, codec,
HDR and audio best first, set the lowest and highest resolution accepted, name codecs never to accept, require
an audio language, and list **preferred release groups** that rank above an otherwise equal release. A profile's
target for video also takes file types (mkv, mp4, avi, wmv, mpeg, ts ...), audio channels, audio and subtitle
languages, audio and video bitrate (kbit/s, within a tolerance) and frame rate. **Language profiles** are made
under Quality profiles, below Target qualities.

A TV library's **Quality** page takes a **target** of picture quality (resolution, video codec, HDR, audio codec,
file type; a part left on "any" is free). Releases that match it are ranked first inside the profile's own
limits. Each episode on the series page shows what the probe measured (resolution, codec, HDR, audio, size,
subtitles) and whether it meets the target. Subtitles count from the file's own tracks and from sidecar files
beside it (`Show - S01E01.eng.srt`); a language profile can require or prefer a subtitle language.

"Quality against the target" in Statistics counts the files that miss the library's cutoff; the cutoff check
refreshes it. The probe reads video files in the background: a first pass over a large library takes hours and
the figures fill in as it goes.

### Upgrades

With a target or cutoff set, a season whose files all exist but fall short of it is hunted for a better
release. A download replaces an episode file only when it is better on resolution, codec, HDR, channels, bitrate
and the other target fields; an equal one is dropped, and the replaced file waits in the recycle bin. Turn
**Upgrades allowed** off in the library's Quality settings to stop it. More: [library-quality.md](../library-quality.md).

## Metadata providers and their order (🟡)

**Settings → Metadata → TV series** orders the providers: the first one that holds a field (plot, genres,
rating, artwork) supplies it. The default is open and fast first (Skyhook, TVmaze, TMDB, TheTVDB, AniList).
Wikidata is listed too: it recognises the same person in TV and music. Speeds behind the default order:
[tv-providers-benchmark.md](../tv-providers-benchmark.md). What each provider carries:
[metadata-providers.md](../metadata-providers.md).

## Importing from Sonarr (🟡)

**Connections → Arr**: the series, the monitored state of every episode and the quality profile of each series
(as mapped in the preview) come over, and Sonarr's Media Management (Episode Title Required, file date,
propers, empty folders...) if ticked. **Import before the first scan**: a series already in Solarr is left as it
is. Details: [arr-import.md](../arr-import.md).

## Under the hood (🔴)

[tv-internals.md](../tv-internals.md).
