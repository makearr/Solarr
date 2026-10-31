# Importing from, and sending back to, the *Arrs

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

## Where it is

`internal/importing/arrimport` (the clients, including `SetMonitored`), `cmd/solarr/adapters/arr*.go` (the walk, the sources, the
monitoring pass, `arrpush.go`), `internal/api/apiserver/arrimports.go` and `api/openapi.yaml` (`ArrSyncOptions.mode`,
`indexers`, `download_clients`; `push_monitoring` on a connection; `POST /arr/instances/{id}/push`).
