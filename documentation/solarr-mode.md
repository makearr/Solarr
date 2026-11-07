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
- **Companion**: `ArrSync` pushes the monitoring of items an import linked, whether or not the connection ticked "send monitoring back". Items not imported from that *Arr are not added to it.

The job executor's old dry-run modes (`observe`, `shadow`, ADR-20) are no longer fed from the config; `mode` now means this setting.
