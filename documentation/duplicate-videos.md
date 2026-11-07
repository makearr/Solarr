# Duplicate videos

Finding the same video stored more than once, in another encode, size or container (ADR-35).

## Hashing

The probe sweep's last pass hashes every video (`mediatool.PerceptualHash`, algorithm version 2, stored beside each hash).
It is built so the same footage at another resolution, frame rate, codec, aspect ratio or a few frames' offset still matches:

1. **Moments by time.** Nine moments at 1/10 to 9/10 of the duration, so a re-encode with another frame rate or frame
   index lands on the same picture. One ffmpeg call per moment reads a window of five frames 0.125 s apart (`fps=8`, so the
   spacing is the same whatever the source's rate) in colour, with the sample aspect ratio applied, at most 426x240 (the
   third moment at most 1280x720 while previews are on, never enlarged); Go then stretches each frame to 128x128 and converts it to grey.
2. **Black bars off.** Per video, the rows and columns at each edge that are mostly (55%) at most 40 grey are bars, up to
   35% of a side. The crop is the lower quartile across all frames, so a logo, subtitles or a dark scene on a few frames
   neither shrinks nor grows it. Letterboxed, pillarboxed and stretched copies reduce to the same picture.
3. **Scene cuts.** A window is split where neighbouring frames change by more than 28 grey on average; only the longest
   shot is kept, so a cut that falls inside one copy's window and outside the other's cannot mix two shots.
4. **Reduce and hash.** The cropped picture is area-averaged to 32x32 and hashed by a 2-D DCT: the 8x8 lowest
   frequencies, one bit each against the median (64 bits). Flat frames (black, fades) are left out. The mean hash is the
   same over the average of every kept frame and only finds candidates (banded index); the frame hashes decide.
5. **Compare.** A moment holds up to five hashes. Two videos match at a moment when any hash of one is within the frame
   distance of any of the other's, so an offset of up to about a quarter second still lines up. Two thirds of at least four
   shared moments must match, the durations must agree (2% or 5 s) and the mean hashes be within the level.

Stash-box compatibility: Solarr's hash is **not** Stash's and is local only. Solarr submits no fingerprints to StashDB
today (`stashbox` only looks scenes up by a file's hashes). If a Stash-compatible pHash is ever needed it must be stored as
its own value beside this one; `media_object.phash` is the local mean hash.

Algorithm version: `video_frame_hash.algorithm_version` (`library.VideoHashVersion`, equal to
`mediatool.PerceptualVersion`; a test pins that). A video with no hash of the current version is hashed again by the next
sweep, so changing the algorithm means bumping the number. Migration 0107 dropped every version-1 hash (and
`media_object.phash` of videos) for that reason.

## Previews

`scan.video_previews` (default off, Settings > General; turning it on turns `scan.video_phash` on) makes two pictures of each
video from the frames the hash reads, so a file is read once: a single frame (the third moment, 3/10 into the video, at most
720p) and a 3x3 sheet of each moment's most detailed frame (cells fitted into 426x240, 1278x720 for 16:9). Both are colour,
in the video's own shape, uncropped, JPEG. They live in `<config dir>/previews/<id/1000>/<id>-frame.jpg` and `-sheet.jpg`
(`internal/platform/previewstore`), belong to the file (`media_object`), and are listed in `video_preview` with the algorithm
version, so a hash bump makes them again. `GET /files/{id}/preview/{kind}` serves them to whoever may read the file; the work
list names the first previewed file of every work (`preview_file_ids`). The web interface shows the frame in the
overview and table of a library and the sheet in file details and look-alike duplicates, unless the person switched
Settings > Web UI > Show thumbnails off. Gap: previews of a deleted file stay on disk.

The pass is a setting, `scan.video_phash` (default off), edited by an administrator under Storage > Duplicates
(`GET/PUT /settings/video-hashing`). Off, nothing new is hashed; hashes already stored still serve the finder, and the
report says so (`hashing_enabled`). The tolerance is `scan.video_phash_frame_bits` (default 10, 2 to 20): the frame
distance at the medium level; exact, high and low scale it to 0, one half and 1.3 times.

Limits: fast action can exceed the cut threshold between neighbouring frames, which leaves a window with one frame (the
offset tolerance is then gone for that moment); hard-coded subtitles that cover more than half a bar row crop the bar away.

## Why not Stash's method

Stash hashes 25 frames (5x5, from 5% to 95%), one ffmpeg call each, tiles them into one image and takes a single 64-bit
pHash of the montage. Measured 2026-11-03 (Go 1.25, ffmpeg, one core of an Intel Core Ultra 7 255H, 25-minute h264 files):

| | Solarr (7 frames) | Stash (25 frames) |
|---|---|---|
| 1080p, per video | 1.45 s | 6.80 s |
| 360p, per video | 0.85 s | 3.00 s |

Grouping the hashes, 125,000 videos: 0.9 s with the banded index (`groupSimilar`) against 16.9 s comparing every pair, as
Stash's finder does; 15,000 videos: 20 ms against 109 ms. Hashing dominates a library's cost by hours, so the method
stays. Stash's levels are kept: the finder takes the same four.

## Levels

`level` on the report: `exact` (0 bits between the mean hashes), `high` (4), `medium` (8, the default), `low` (10).
The per-frame bound is a quarter wider (`frameBitsFor`, scaled by the tolerance setting), and two thirds of at least four shared moments must still match.

## Finding and resolving

- One library: `GET /categories/{id}/similar-videos`, on the library's Duplicates page; resolving needs manage access.
- Every library: `GET /duplicates/similar-videos`, Storage > Duplicates, administrators only.
- `POST .../duplicates/resolve` (one library) and `POST /duplicates/similar-videos/resolve` (all) keep the chosen file
  and, per other file, either merge it into the kept one (every path becomes a link; the old file waits in the recycle
  bin) or recycle it. A merge across libraries is refused: a merged file would belong to two libraries.

A file linked at several paths (one media object, many paths) is one video in a group with a `path_count`, not several copies; a group needs two different files. Found live on the docu library, where 42 of 238 groups were one file listed repeatedly.

## At import

A downloaded TV episode is hashed (same seven moments) and compared with the hashed files of the series it resolved to,
at the medium level (`SimilarVideos.MatchInWork`). A match is the episode the held file covers, whatever the release
named it, so a release labeled S01E02 that shows S01E01 never fills S01E02.

- The held file below its library's cutoff: the download is placed beside it and the cutoff step keeps the strictly better
  file (a tie keeps the held one), recycling the other.
- The held file at its cutoff: the unit is skipped with `batch.ReasonDuplicate`.
- Each placed file keeps its hash, so the check works without the background sweep for files imported later.

Settings (`GET/PUT /settings/import-duplicates`, administrators, Storage > Duplicates): `import.duplicate_check` (on) and
`import.duplicate_blocklist` (off). Blocklisting covers a download skipped as a duplicate and one the cutoff step recycled
as no better (reason `REJECTED_WORSE`, 30 days). Off, a duplicate download is only removed from the client; a later search
can grab the same release again. Music (Chromaprint is not stored for the library yet) and movies are not covered.

Two limits seen live (2026-11-04, real SABnzbd grabs): a different cut of an episode (a release running about 4% longer than
the held file) is not matched, by the duration gate above and because its frames land at other moments; and SABnzbd's own
duplicate detection refuses a second release of the same episode, so a repeat download of one episode needs another client.

The similarity level opens on **Exact** and remembers the one last chosen (a browser preference).
