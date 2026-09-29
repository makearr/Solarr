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
| Delete Empty Folders | off | every disk scan, from the empty folders the sweep met; and when files are deleted (remove album/track, import revert) | `music.Housekeeping`, `library.RecycleBin.Put` |
| Unmonitor Deleted Tracks | off | a **full** scan: an album holding a file the scan found missing is unmonitored. Only a full scan is evidence of absence (section 10.4) | `library.Desired.UnmonitorItemsLosingFiles` |
| Propers and Repacks | Prefer and Upgrade | search: a proper or repack ranks after quality, before the additive score. *Upgrade* vs *Do not Upgrade Automatically* differ only once upgrades are evaluated (stage 6); today both prefer | `music.withRevision` |
| Rescan Artist Folder after Refresh | Always | after an artist refresh (the artist page's Refresh is manual; the weekly *Refresh artists* task is not) and after enrichment refines an album, its folders are rescanned: always, only after a manual refresh, or never. Files are attached to a newly enriched tracklist either way, from where scan or import placed them (`file_track_claim`) | `app.realigner`, `music.AlignClaimedFiles` |
| Change File Date | None | import, and every scan of a writable root: a file's date becomes its release date (the `date` tag). Never on a read-only root (Invariant 69) | `music.Committer`, `music.Consumer` |
| Recycling Bin Cleanup | 7 days | how long recycle bin keeps what this library removes; the *Clean recycling bin* task (daily 02:30) purges what is past it | `adapters.recyclingFor`, `library.RecycleBin.Sweep` |
| Set Permissions, chmod Folder, chown Group | off, 755, unchanged | import and library rename: the file gets the folder mode without execute bits, every folder between it and the root gets the folder mode, both the group | `mover.ApplyPermissions` |

Rules worth knowing:

- **The recycling bin is recycle bin** (section 10.8): there is no bin path. Removed files go to
  `<root>/recycleBin/<date>/` on the same filesystem. A file in two libraries is kept as long as
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

## Write MusicBrainz IDs into files

On by default, per library (revision D3). The `tag_writeback` task (hourly, and after every
enrichment run that refined albums) fills a file's tags with the release group, release,
recording and album artist ids Solarr identified it by: `musicbrainz_releasegroupid`,
`musicbrainz_albumid`, `musicbrainz_trackid` and `musicbrainz_albumartistid`, Picard's names,
which Navidrome and Lidarr read.

- **Fill-only.** A key the file already has is never replaced, even with a different value.
- **Only once the album has a release**: a file whose album is identified to a release group
  but no edition yet waits for enrichment to choose one.
- **Never on a root that is read-only, offline or not writable**, whatever the library says.
- **Once per file**, recorded in `tag_writeback` (`library.db`): an MP3's recording id is a UFID
  frame ffprobe cannot show, so re-reading would rewrite it for ever. A failure is recorded too.
- A run writes at most 2,000 files; a large library is written over successive hours.
- The written file keeps its identity: a known path keeps its `media_object`.
