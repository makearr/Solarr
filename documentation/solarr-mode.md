# Solarr mode

One instance-wide setting, `general.solarr_mode`, in Settings, General. `SOLARR_MODE` (or `mode:` in `config.yml`) writes it at every start; the interface changes it while running.

| Mode | Library folders | Indexer searches | Downloads | Monitoring sent to the *Arrs |
|---|---|---|---|---|
| `normal` | written | yes | yes | connections that asked |
| `read_only` | refused | yes | yes | connections that asked |
| `pause_downloads` | written | yes | none sent | connections that asked |
| `observe` | refused | none | none sent | none |
| `arr_companion` | refused | none | none sent | every enabled connection |

Where each is enforced:

- **Read-only**: `fsx.Guarded` wraps the process filesystem and refuses every write, rename, delete, link and permission change under a root folder (`cmd/solarr/app/solarrmode.go`). Nothing else is gated, so an import, rename or move fails with a read-only error rather than being skipped.
- **Searches**: `indexerConns` and `searchConns` return no indexers, which every automatic and interactive search shares.
- **Downloads**: `dispatch.Dispatch.Paused` refuses a send and keeps held releases waiting; an interactive pick answers "Downloads are paused".
- **Companion**: Solarr follows the *Arrs and never searches or downloads itself.
  - *Add here, search there.* An item added in Solarr (a movie by TMDB id, a series by TVDB id) is looked up in the library's connected Radarr or Sonarr (the one for that library, else the only enabled one of the app). Already there: it is set monitored and the *Arr is asked to search. Missing: it is added monitored with a search, under the quality profile and root folder most of that *Arr's items use (else its first). The *Arr's item is linked to Solarr's, so monitoring keeps following. Music, books and adult are not added this way yet.
  - *Follow the *Arr.* On the monitoring schedule Solarr sends the monitoring of linked items back and then, for a connection whose import you have run and finished, imports what the *Arr monitors and Solarr lacks.
  - *Files.* The *Arr downloads and renames; Solarr finds the file at the next scan of the root folder.

The job executor's old dry-run modes (`observe`, `shadow`, ADR-20) are no longer fed from the config; `mode` now means this setting.
