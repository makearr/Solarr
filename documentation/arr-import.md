# Importing from, and sending back to, the *Arrs

> **Level: 🟡 Advanced.** Bring over a Lidarr, Sonarr, Radarr or Whisparr setup.

A connection (Connections, or step 2 of the setup assistant) is one Sonarr, Radarr, Lidarr, Whisparr or Prowlarr: its address
and API key. Readarr connects and waits for its own import. The same options panel is on the Connections page and in the
setup assistant.

## What an import brings in

Each run chooses; nothing is imported that is not ticked.

| Choice | What it does |
|---|---|
| **What the *Arr holds** | Its items become works and follows here, as the add dialog would add them: monitored ones, unmonitored ones (they stay unmonitored), a Lidarr album filter, its quality profiles mapped to ours, and its Media Management as a library's File Management. A preview counts them first. |
| **Its paths** | Its root folders are added here and attached to a library, with path rewrites for how the *Arr's paths read here (`/porn` as `/mnt/media/porn`). A scan follows. |
| **Only the monitoring status** | Nothing is added. What is already here, matched by the same ids, is monitored or not as the *Arr says, both ways. An item not held here is counted as skipped. |
| **Its indexers** | Its Newznab and Torznab indexers are added with their address and key, switched as they are there. |
| **Its download clients** | The ones Solarr has a client for, added **switched off**: an *Arr hides a client's password. |

Items are matched by the id each app keeps: MusicBrainz for Lidarr's artists and albums, TheTVDB for Sonarr, TMDB for Radarr,
StashDB for Whisparr (a scene's `stashId`, a performer's and a studio's `foreignId`). Whisparr's scene list is one response of
over a hundred megabytes that takes minutes, so it is read as a stream with a twenty-minute limit.

Whisparr: a monitored **performer** or **studio** becomes a follow (their scenes arrive as the follow runs), a **scene** is added
by its StashDB id, monitored as there ([adult.md](adult.md)).

## Sending monitoring back

A connection may send the monitored flag of what an import matched back to its *Arr (**off** until its owner turns it on, at the
connection or in the setup assistant). It is the one thing Solarr writes to an *Arr; ADR-52 had none.

- Only items an import linked are touched (`arr_link`), and only their `monitored` field: the resource is read, that field
  changed, and written back whole.
- Solarr's state stands: a work is monitored when any of its wanted items is, a person or studio when someone follows them. What
  differs at the *Arr is written over it. To take the *Arr's state instead, run an import for the monitoring status only.
- It runs every six hours (*Send monitoring back to the *Arrs* in the scheduler), and *Send now* on the connection does it at once;
  the connection shows when it last did and how many items changed.

## Living beside the *Arrs

In **`arr_companion`** mode ([solarr-mode.md](solarr-mode.md)) Solarr never searches or downloads. An item added in Solarr is
handed to the connected Radarr, Sonarr, Lidarr (an album or artist by MusicBrainz id) or Whisparr (a scene, by its StashDB or
ThePornDB id) under that *Arr's most-used profile and folder, and Solarr follows the *Arr's monitoring and finds the file at
the next scan.

## Where it is

`internal/importing/arrimport` (the clients, including `SetMonitored`), `internal/importing/arrmap` (the walk, the monitoring
of what is held, `arrmap.Importer`), each type's `arrimport.go` (`music.LidarrImport`, `tv.SonarrImport`, `movie.RadarrImport`,
`adult.WhisparrImport`: what an item of that app becomes), `cmd/solarr/adapters/arr*.go` (the run and its recording, `arr_link`,
the preview's sums, `arrpush.go`), `internal/api/apiserver/arrimports.go` and `api/openapi.yaml` (`ArrSyncOptions.mode`, `limit`,
`indexers`, `download_clients`; `push_monitoring` on a connection; `POST /arr/instances/{id}/push`).

Lidarr's import adopts from Lidarr's record and calls no metadata source (16k albums at MusicBrainz's 1/s would take hours);
Sonarr, Radarr and Whisparr add through the type's own add, which reads its sources.

## Merging two libraries

Two libraries of one media type can be merged (Settings, Libraries, *Merge into…*; `POST /categories/{id}/merge`): the wanted
items, requests, collections, import sessions, moves and follows of the first become the second's, its root folders are
attached to the second, its access grants are dropped and it is removed. A work both held is wanted once. Files stay where
they are; each library's rollups are recomputed.

## Prowlarr sync

A Prowlarr connection keeps indexers in step instead of importing a library. Under the connection, choose what follows:
active indexers, paused indexers (added paused), only indexers with a category of a media type Solarr serves (movies,
audio, TV, adult, books; not console or PC), and whether to sync every hour. **Sync now** runs it at once; the task
"Sync Prowlarr indexers" runs the automatic ones. The followed indexers appear under Connections > Indexers marked
"from Prowlarr". Each is the site behind the Prowlarr indexer (NZBgeek is the NZBgeek indexer itself), so Solarr can
take over from the *Arrs. Prowlarr hides every indexer's API key, so one with a hidden key stays "via Prowlarr", searched
through Prowlarr's per-indexer address, until the key is entered on its row ("Use it directly"). Sites without a key, and
public sites Solarr has its own indexer for, switch at once. The option "Use Prowlarr itself as the indexer" (off by
default) keeps every indexer on Prowlarr's address and key. The option "FlareSolverr" adds an address to the proxy list;
each indexer chooses it under its own proxy.

Priority comes from Prowlarr when an indexer is added and is Solarr's after that. A pause follows Prowlarr only when
Prowlarr's own flag has changed since the last sync, so pausing an indexer here is not undone by the next sync.

### Proxies

Connections > Indexers > Proxies lists the proxies an indexer can go through: HTTP, SOCKS5 and FlareSolverr. Each indexer
picks one. With the Prowlarr option "Proxies" on, Prowlarr's FlareSolverr and credential-free HTTP/SOCKS5 proxies are
copied into the list and given to the indexers that share a tag with them in Prowlarr; a proxy you chose by hand is kept.
Prowlarr's own FlareSolverr address is often `http://localhost:8191/`, which is that host's localhost: change the copied
entry to an address Solarr can reach. A proxy an indexer uses cannot be removed.

FlareSolverr is asked only when a site answers with a Cloudflare or DDoS-Guard challenge; its clearance cookies and user
agent are then reused for the site's next requests, so downloads still go straight to the site.
