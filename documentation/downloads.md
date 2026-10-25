# Downloads: what is sent, and what waits

## Download clients

Every kind of client Lidarr and Sonarr offer is offered here, and none is configured until the person adds one
(Connections -> Download clients). `GET /downloaders/schema` lists the kinds, as Sonarr's `/downloadclient/schema`
does: each kind's protocol, what it signs in with, and the settings it needs beyond the shared ones. The add dialog
is drawn from it, and a client is refused on write when it lacks what its kind needs (an address, a blackhole's
folders) or names a kind Solarr cannot drive.

| Kind (`impl`) | Protocol | Signs in with | Its own settings | Category |
|---|---|---|---|---|
| SABnzbd (`sabnzbd`) | usenet | API key | | category |
| NZBGet (`nzbget`) | usenet | user name, password | | category |
| Usenet Blackhole (`usenetblackhole`) | usenet | | NZB folder, watch folder | |
| qBittorrent (`qbittorrent`) | torrent | user name, password | | category, created on first send |
| Transmission (`transmission`) | torrent | user name, password | URL base (`/transmission/`) | a folder below its download folder |
| Vuze (`vuze`) | torrent | user name, password | URL base (`/transmission/`) | as Transmission, through its Web Remote |
| Deluge (`deluge`) | torrent | web UI password | | Label plugin label, created on first send |
| rTorrent (`rtorrent`) | torrent | user name, password (HTTP) | URL path (`RPC2`) | label (`custom1`) |
| µTorrent (`utorrent`) | torrent | user name, password | | label |
| Flood (`flood`) | torrent | user name, password | | tag |
| aria2 (`aria2`) | torrent | RPC secret | RPC path (`/rpc`) | none |
| Torrent Blackhole (`torrentblackhole`) | torrent | | torrent folder, watch folder, save magnet links | |
| slskd (`slskd`) | ddl | API key | | |

**One base for every kind** (`internal/acquiring/download`, Sonarr's `DownloadClientBase` and its
`TorrentClientBase`/`UsenetClientBase`). A kind declares a `Definition` and implements only what differs:
`TorrentClient` (`AddTorrentFile`, `AddMagnet`, ...), `UsenetClient` (`AddNZB`, ...) or `DirectClient`
(`AddDirect`, ...), each with `Probe` for its connection test. `NewTorrent`, `NewUsenet` and `NewDirect` supply the
rest: the info hash every torrent is tracked by and the magnet-or-file choice, refusing what is plainly not an NZB,
reading a direct release, queue totals, never asking a client to pause nothing. The registry
(`cmd/solarr/adapters/downloadclients.go`) lists the kinds; searching, polling and the connection test build every
client through it.

What a kind cannot do: aria2 and rTorrent never delete a removed torrent's files themselves (rTorrent's are deleted
when ruTorrent's erasedata plugin is installed); NZBGet keeps the files of a job it finished; a blackhole and slskd
cannot pause, and a blackhole forgets a download only by deleting it. A blackhole download counts as finished once its
files have not changed for a minute, and its watch folder becomes a download folder when it is added.

**slskd** takes a direct release: JSON naming the peer and the files of one of its folders
(`{"source": "user", "files": [{"path": "@@music\\Artist\\Album\\01.flac", "size": 123}]}`). No indexer answers
one yet, so an slskd client receives nothing until a Soulseek search source exists.

## Missing albums are searched on their own

Two scheduled tasks (Settings -> Scheduler) do what Lidarr leaves to a person pressing *Search all*:

- **Plan what is wanted** (every 5 minutes, advanced) reads every album whose state or search record
  changed, plus a slice of the rest so a whole pass takes a day, and writes the hunt queue: which
  monitored MISSING or PARTIAL albums may be searched, how urgently (the priority ladder below), and
  from when. An album with a known street date waits until 12 hours after it; one just searched
  waits 6 hours, then 12, 24, doubling to a week. Tracks are never searched on their own.
- **Search for what is missing** (every 15 minutes by default, **Search every** on the Automatic searching card)
  searches the most urgent eligible items, least recently searched first, and sends the best release as an
  automatic grab. Each run spends its part of **Automatic searches per hour** (Settings -> Priority, default 30,
  0 = off: a quarter of it every 15 minutes), dealt to the libraries by their **search share** (default 1 each, 0
  none) by a weighted round robin that carries over between runs; what one library cannot use goes to the others.
  An item with a download already on its way costs nothing and is looked at again in an hour. Every search counts
  toward the backoff, whoever started it.

**A person's search** is one set of routes for every media type: `POST /works/{id}/search` (interactive:
decisions recorded, nothing sent), `POST /works/{id}/grab` (automatic: the best release is sent),
`GET /works/{id}/candidates` and `.../candidates/{decisionId}/send|blocklist`. Each takes a scope: `group` (a
season's ref), `part` (one episode's id; its season is searched, narrowed to it) or neither (an album). The work's
type answers it through `media.Searching`; music searches whole albums only, as Lidarr does, and refuses a `part`.
A search body replaces the work's own words by the keys of the type's `search_terms` (music: artist and album).

**RSS sync** (every 15 minutes, as Lidarr's) reads each indexer's newest 100 audio releases
(each indexer's **Music categories**, Connections -> Indexers, default 3000 Audio -- without a
category a feed is mostly television; searches use the same categories), groups them by the artist they
name, and judges each group against that artist's wanted albums exactly as a search would. The best
accepted release is sent as an RSS grab. Nothing is searched, and an album's search backoff is left
alone. Artists are matched by name ignoring case; an alias or a differently spelled name is not yet
matched.

**Free space gates automatic downloads** (ADR-12). The hunt, RSS sync and Search all never send a
release larger than the library's headroom: the most free space above its reserve on a root that
takes new files (Storage -> Folders; measured by the capacity task every 6 hours), less what the
library's downloads already on their way will take. A library
with no headroom is skipped by the hunt without spending its budget, and the dashboard says
automatic downloads are paused. Your own grabs are never gated.

Every search asks all enabled indexers at once and waits at most **20 seconds** for them (ADR-37):
a dead or slow indexer bounds a search instead of adding to it. The album page and Interactive
search name each indexer that failed or did not answer in time.

An album's page says, under Search, when it was last searched and when the automatic search will
look again -- or why it will not: a download on its way, a release held for room, no indexer,
searching switched off, no free space, not yet planned, or waiting for its release date.

Adding from **Discover** with **Start search for missing albums** ticked (on by default, remembered
per person) searches an album at once, as Lidarr does. For an artist it adds their albums now rather
than at the 12-hourly subscription expansion, then searches the missing ones. Monitoring an album
from its page does not search; the hunt picks it up within 15 minutes.

Once a release is in a download client, the client decides the order. So Solarr sends each client
only as many releases as its **Downloads at once** (per client, default 10; ADR-57) and keeps the
rest **held**, in its own order, until a download finishes or is removed.

## Who asked decides whether a release waits

| Grab | Examples | Full client |
|---|---|---|
| **Yours** | an album's *Search*, a release picked in *Interactive search*, a request | sent anyway, unless the client's **Always send my grabs** is off |
| **RSS** | a release an RSS sync found for a wanted album (M1c) | sent anyway, unless **Always send RSS grabs** is off |
| **Automatic** | Wanted's *Search all*, the search after a failed download, the planner (M1c) | held |

Both boxes are on by default (2026-09-29). An interactive pick counts as yours.

## The order held releases go in

Your grabs first, then RSS, then new albums (released within 7 days, or of unknown date), then
older ones (ADR-36's ladder: `priority.Compute` with the user-request, RSS, new-release and gap
weights). Within one, the longest waiting goes first. Each is checked again just before it is
sent: still monitored, not blocklisted, nothing already on its way for the album; otherwise it is
dropped and the album searched afresh later. One release per album is held; a newer decision
replaces it. A held release carries what sending it needs (indexer, guid, title, link, protocol, size, score), so it
goes without reading the search that found it; an acquisition likewise keeps its own guid, title and size.

**Activity → Queue** lists the held releases below the downloads, in send order, with their size,
who asked, the indexer and how long they have waited. The album page says "Waiting for room in
the download client".

## Stalled downloads are removed (per client, off by default)

A client's Options can switch on **Remove stalled downloads**. Every *check* minutes (default 15) the poll compares a
running download of Solarr's own with the last check (`poll.checkStall`, state in `acquisition_stall`): one that has not
moved gets a strike, progress clears them. After *strikes* in a row (default 5), or *hours* without progress (default 2)
whatever the strikes, the download takes the path of a download the client failed: removed with its files, its
release blocklisted by guid and content for 30 days, the album or season searched again, History saying "stalled".
Queued and paused downloads are never struck, and a paused one's clock starts again when it runs, so a person's pause
is never undone; downloads over the size limit (default 50 GB, 0 none) and clients with the switch off are left alone.

**What finished is kept.** Before a stalled download is removed, a client that can say which of its files are complete
(`download.FinishedFiles`; qBittorrent, from each file's progress) has those files imported on their own: the import
is limited to them (`importjob.Payload.Only`, `inspect.WalkOptions.Only`; no archive is opened), the download stays in
the client meanwhile, and once the import has placed something the release is blocklisted, the download removed with
what it still held, and the item searched for again for the rest (`poll.finishedImported`). Only a download with a
folder of its own qualifies (a multi-file torrent). qBittorrent keeps unfinished torrents in its own folder when
"Keep incomplete torrents in" is set (`download_path`): that folder must be visible to Solarr through the client's
path mapping and lie in a download folder, or the failure says why the finished files stayed behind. SABnzbd is not
asked: a usenet job fails on missing articles rather than stalling, and its finished parts are often RAR volumes.

## What a finished import decides (`poll.followImport`)

A unit counts as placed only when it was committed **and** has a work (`import_unit.work_id`); the download is then
imported and, if the client says so, removed. A unit its library could not take (no root folder, none with room, or
none online: `placer.ErrNoRoot`, `music.ErrNoPlacement`) stays committed but unplaced. The download is kept, never
blocklisted or removed, with the note "no root folder of its library can take it", and a health warning links to
Folders. The registered **Retry imports a library could not take** task (twice an hour, `importjob.RetryJob`) places
those units with `ManualCommitter.PlaceUnplaced` once a root can, and the next poll imports the download. A TV or movie
unit keeps the work it resolved to in its candidates (`batch.WaitToPlace`), so the retry does not identify it again.
A TV unit waits the same way while an episode in it is still titled TBA and aired less than 48 hours ago, as the
library's **Episode Title Required** asks (always, the default; only for whole-season releases; never), so the file is
named with its real title (`tv.titleWait`).
A unit held for a person (a rule refused it) waits on the Import page. A release that brought nothing usable (every
unit skipped, for example **nothing new or better** than the library holds, `batch.ReasonNothingNew`, or failed) is
failed: blocklisted, removed and searched again. If a retry finds a unit's files gone or nothing new any more, the unit
is closed, so its download fails instead of waiting forever (user, 2026-10-24).
