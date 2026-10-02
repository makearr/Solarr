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
| Merge Artists Stored Under Several Names | off | every disk scan: a local artist named like an identified artist (or one of its MusicBrainz aliases) joins it; local artists named alike (case and punctuation ignored) join the one with most albums. Never two identified artists, never an artist with work in another library; files stay put | `library.People.PlanArtistMerges`, `MergeArtists` |
| Propers and Repacks | Prefer and Upgrade | search: a proper or repack ranks after quality, before the additive score. *Upgrade* vs *Do not Upgrade Automatically* differ only once upgrades are evaluated (stage 6); today both prefer | `music.withRevision` |
| Rescan Artist Folder after Refresh | Always | after an artist refresh (the artist page's Refresh is manual; the weekly *Refresh artists* task is not) and after enrichment refines an album, its folders are rescanned: always, only after a manual refresh, or never. Files are attached to a newly enriched tracklist either way, from where scan or import placed them (`file_track_claim`) | `app.realigner`, `music.AlignClaimedFiles` |
| Change File Date | None | import, and every scan of a writable root: a file's date becomes its release date (the `date` tag). Never on a read-only root (Invariant 69) | `music.Committer`, `music.Consumer` |
| Recycling Bin Cleanup (advanced) | 7 days | how long recycle bin keeps what this library removes; the *Clean recycling bin* task (daily 02:30) purges what is past it, and then the oldest while a space limit is passed (Storage → Recycle bin: all bins together, 0 by default, and 10% of each root) | `adapters.RecyclingFor`, `library.RecycleBin.Sweep`, `settings.RecycleBinLimits` |
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

## Tags written into files

Per library, on the **Naming** page beside the file name formats (revision D3, widened
2026-09-29). The `tag_writeback` task writes them once enrichment has identified a file: it runs
after every enrichment run that refined albums, after every cover sweep, when the options are
saved, and hourly.

| Option | Default | Writes |
|---|---|---|
| Write MusicBrainz IDs | on | `musicbrainz_releasegroupid`, `musicbrainz_albumid`, `musicbrainz_trackid`, `musicbrainz_albumartistid` (Picard's names, which Navidrome and Lidarr read), **fill-only**: a key the file has is kept |
| Replace existing MusicBrainz IDs | off | the same ids, overwriting a value that differs from the current one (only with the first) |
| Write other tags | off | `album`, `albumartist`, `title`, `tracknumber`, `discnumber`, `date` (the release's year), `genre` (MusicBrainz genres joined by `; `), overwriting what the file says. No track artist: Solarr keeps no per-track credits yet |
| Embed cover art | off | the fetched album cover as a JPEG front cover, only into a file that carries no picture |
| Navidrome profile (D10) | off | also fills `musicbrainz_artistid`, `artist`, `artists` and `albumartists` (fill-only: what a file has is kept) and writes a `cover.jpg` into each album folder that has no cover file (`cover`/`folder`/`front`, jpg or png); never replaces either | `music.TagWritebackJob` |

- **Only the library's own root folders**, never a download folder, and never a root that is
  read-only, offline or not writable, whatever the library says (Invariant 69).
- **Only once the album has a release**: a file identified to a release group but no edition
  waits for enrichment to choose one.
- **Written again only when something changes.** `tag_writeback` (`library.db`) keeps a digest
  of what was last asked of each file; new ids, metadata, a new cover or changed options make it
  differ. The helper compares with what the file holds and saves only when a value changes.
- An album showing its artist's photo has no cover of its own; nothing is embedded for it.
- A run writes at most 2,000 files; a large library is written over successive runs.
- The written file keeps its identity: a known path keeps its `media_object`.
