# File management

Sonarr's *Media Management → Folders, File Management, Permissions*, per library (asked for
2026-09-19, answered 2026-09-24). Library → Configuration → **File management** (normal level; Propers, Rescan,
Change file date and Permissions from advanced),
`GET/PUT /categories/{id}/file-management`, table `category_file_management` in `settings.db`. A
library that never saved keeps Sonarr's defaults. Exported with the library in the settings
document (`categories[].file_management`).

| Setting | Default | When it acts | Where |
|---|---|---|---|
| Create Empty Artist Folders | off | every disk scan: a monitored artist of this library with no folder on any of its roots gets one, on the root an import would choose (affinity, then most free space) | `music.Housekeeping` |
| Delete Empty Folders | off | every disk scan, from the empty folders the sweep met; and when files are deleted (remove album/track, import revert) | `music.Housekeeping`, `library.Quarantine.Put` |
| Unmonitor Deleted Tracks | off | a **full** scan: an album holding a file the scan found missing is unmonitored. Only a full scan is evidence of absence (section 10.4) | `library.Desired.UnmonitorItemsLosingFiles` |
| Propers and Repacks | Prefer and Upgrade | search: a proper or repack ranks after quality, before the additive score. *Upgrade* vs *Do not Upgrade Automatically* differ only once upgrades are evaluated (stage 6); today both prefer | `music.withRevision` |
| Rescan Artist Folder after Refresh | Always | after an artist refresh (the artist page's Refresh is manual; the weekly *Refresh artists* task is not) and after enrichment refines an album, its folders are rescanned: always, only after a manual refresh, or never | `app.realigner` |
| Change File Date | None | import, and every scan of a writable root: a file's date becomes its release date (the `date` tag). Never on a read-only root (Invariant 69) | `music.Committer`, `music.Consumer` |
| Recycling Bin Cleanup | 7 days | how long quarantine keeps what this library removes; the *Clean recycling bin* task (daily 02:30) purges what is past it | `adapters.recyclingFor`, `library.Quarantine.Sweep` |
| Set Permissions, chmod Folder, chown Group | off, 755, unchanged | import and library rename: the file gets the folder mode without execute bits, every folder between it and the root gets the folder mode, both the group | `mover.ApplyPermissions` |

Rules worth knowing:

- **The recycling bin is quarantine** (section 10.8): there is no bin path. Removed files go to
  `<root>/quarantine/<date>/` on the same filesystem. A file in two libraries is kept as long as
  either wants and its folders removed only when both say so.
- **The root folder itself is never touched**: no mode, no group, never deleted. Host
  permissions are reported, never corrected (section 20.2); `chmod Folder` must leave the owner
  `7`, or Solarr would lock itself out.
- **Hardlinks share the source's inode**, so a mode, group or date set on an imported hardlink
  changes the download too, as in Sonarr. A symlinked import is left alone: its bytes are the
  source's.
- **An artist folder survives Delete Empty Folders while Create Empty Artist Folders is on**, as
  Sonarr keeps a series folder; empty album folders always go.
- **A group name** is looked up in the container's own group database; use the gid on network
  filesystems or when the name exists only on the host.
- A permission or date that cannot be set is logged, never a failed import.
