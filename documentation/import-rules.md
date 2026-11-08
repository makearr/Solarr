# Import rules

> **Level: 🟡 Advanced.** What an import accepts, and why.

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
| EpisodeTitle (never / bulk season releases / always; TBA or empty, aired within 48 h) | `rules.EpisodeTitle` | TV importer, by the library's **Episode Title Required** (always, the default; whole-season releases only; never): see `tv.IsTBA`, `TitleWaitAfterAiring` |
| MatchesGrab (episodes outside the grabbed release) | `rules.NotInRelease`, `rules.Unexpected` | pending |
| MatchesFolder (episodes unexpected by the folder name) | `rules.Unexpected` | pending (unimplemented in Radarr) |
| SameEpisodesImport (the file on disk holds more episodes) | `rules.ExistingHasMore` | pending |
| AlreadyImported (history by download id) | acquisition state per client reference | the poller |
| FreeSpace / MinimumFreeSpaceWhenImporting | the per-root free-space gate (`min_free_bytes`) | placement |
| Upgrade (not a quality or revision upgrade) | the strict-upgrade judge (Invariant 72) | search, RSS and the cutoff check |
| GrabbedReleaseQuality | log only upstream | not ported |
| UnverifiedSceneNumbering | TheXEM's map, read before placing (`sceneNumbers.partKeys`) | not ported as a rejection |

A file ffprobe cannot read is let through (upstream holds it): the tool is optional.

## Files a series' numbers cannot place

A scan takes a second look at a series folder's leftovers (`internal/media/tv/secondlook.go`). A file with no number is placed
when its name, or the part after the last " - ", is the exact title of one episode; two episodes of that title, or a near match,
stay in Review. A file numbered past its season's end (S01E19 of a season of 18) is never placed by itself: Review offers the
episode the number counts on to, "Duplicate episode: a second copy of S02E01" when that one is held, and picking it (with the
whole folder if asked) adds the file there, where the Duplicates report lists both copies and can link them.
