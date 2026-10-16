# Import rules

What Radarr and Sonarr check before they import a downloaded file, and where each check lives in Solarr
(`internal/importing/rules`, ported from their import specifications; `ATTRIBUTION.md`). Every rule is a pure function of facts
the importer passes in and answers with a `model.Rejection` or nothing.

| Radarr / Sonarr rule | Solarr | Where it is applied |
|---|---|---|
| Hidden `._` file | `rules.HiddenFile` | `inspect.Classify`: junk, never imported |
| Dangerous (`.arj .lnk .lzh .ps1 .scr .vbs .zipx`) and executable (`.bat .cmd .exe .sh`) files | `rules.Extension` | `inspect.Classify`: junk |
| User-rejected extensions | `rules.Extension` with a list | not yet a setting |
| Archive, dangerous or executable file left when nothing was imported | `rules.ArchiveFound` | to explain an empty import |
| NotSample (runtime under 15 s / 90 s / 5 min / 10 min by the expected length; zero length; `.flv .strm .iso .img .m2ts` skipped) | `rules.Sample`, `rules.RejectSample` | TV importer (`Importer.screen`); the movie importer |
| HasAudioTrack | `rules.HasAudio` | TV importer; the movie importer |
| NotMultiPart (movie: `cd1`, `part 2`, `disc A` beside its sibling) | `rules.MultiPart` | the movie importer |
| NotUnpacking (`_UNPACK_`, `_FAILED_` folders) and the folder-name cleanup | `rules.Unpacking`, `rules.CleanFolderName` | TV importer; the movie importer |
| ShouldDeleteFolder (only samples left, no archive over 10 MB) | `rules.FolderRemovable` | decides a source folder may go |
| FullSeason (one file named as a whole season) | `rules.FullSeason` | TV importer |
| SplitEpisode (one episode split over files) | `rules.SplitEpisode` | TV importer |
| AbsoluteEpisodeNumber (anime, naming needs it, recently aired) | `rules.MissingAbsolute` | pending the naming flag |
| EpisodeTitle (never / bulk season releases / always; TBA or empty, aired within 48 h) | `rules.EpisodeTitle` | pending a setting; open question in `private/questions.md` |
| MatchesGrab (episodes outside the grabbed release) | `rules.NotInRelease`, `rules.Unexpected` | pending |
| MatchesFolder (episodes unexpected by the folder name) | `rules.Unexpected` | pending (unimplemented in Radarr) |
| SameEpisodesImport (the file on disk holds more episodes) | `rules.ExistingHasMore` | pending |
| AlreadyImported (history by download id) | acquisition state per client reference | the poller |
| FreeSpace / MinimumFreeSpaceWhenImporting | the per-root free-space gate (`min_free_bytes`) | placement |
| Upgrade (not a quality or revision upgrade) | the strict-upgrade judge (Invariant 72) | search, RSS and the cutoff check |
| GrabbedReleaseQuality | log only upstream | not ported |
| UnverifiedSceneNumbering | needs TheXEM | not ported |

A file ffprobe cannot read is let through (upstream holds it): the tool is optional.
