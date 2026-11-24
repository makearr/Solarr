# Transcoding library files

Solarr converts audio and video files already in a library to the library's target quality with the ffmpeg in the
image, as a background task at the lowest priority (`nice 19`, `ionice` idle). The master switch is off by default.

## Pipeline

1. **Analyse** (`transcode.analyze`, one job per enabled library): judges every measured file against the target and the
   target's setup from the facts the probe stored, reading no disk. A file is `needed`, `not_needed` (every skip rule
   passes) or `blocked` (for example a Dolby Vision profile 5 file). Reasons are structured data. A file the probe has
   not read yet waits for the next run. A new setup revision or target judges everything again and clears the
   attempts of files that failed.
2. **Dispatch** (`transcode.dispatch`, the registered task `transcode`, every five minutes and after each conversion):
   settles what a restart interrupted (once), queues the analyses and starts as many files as the lanes have room for.
   Libraries by priority, then least recently served; inside a library by its order; interrupted files first; never
   the file that ran last while another waits.
3. **Convert** (`transcode.file`, one job per file, never retried by the queue): probes the streams, analyses again with
   the real streams, checks free space against the root's reserve, picks the engine, then runs ffmpeg into
   `<root>/recycle-bin/.transcode/<id>.<pass>.<ext>`. A GPU step that fails in the first 5 % hands the file to the next
   step (CPU decode, then software when the setup allows the fallback); a later failure is an error.
4. **Check** (`Check`): streams, duration, size against the original and against the target's range. A result that is
   too large goes down the setup's ladder (a higher CRF or a lower bitrate) for up to the configured extra passes, then
   fails with the code of the first failed check.
5. **Replace**: the original goes to the recycle bin (reason `transcoded`, own retention), the result takes its place,
   and one library transaction gives the media object its new size, hash and path. Each stage is journalled on the item.

## Lanes and pausing

Three lanes (`cpu`, `gpu`, `audio`) each have a limit in Settings; a second set applies while the web player converts,
and ffmpeg processes beyond it are paused (SIGSTOP). Pausing the `transcode` task in Settings > Scheduler stops running
conversions (they return as interrupted, no attempt spent) and holds the queue.

## Where a conversion shows

Settings > Scheduler lists a running conversion with the name of its file and the share of its duration done. A finished
one adds a `transcoded` row to the History page (file name, engine, bytes saved) and a `transcode:<id>` event on the
library topic that the interface may use to refresh.

## Originals and restore

The original of a converted file stays in the recycle bin for the setup's retention (Settings > Transcode > originals;
0 days follows the library's own bin retention). When the bin holds more converted originals than the size cap, the oldest
are purged after each conversion until it fits (0 = no cap). Restoring puts the converted file in the bin (reason
`transcode_reverted`, the library's retention), brings the original out, gives the library the original's size, hash and
path, and rests the file as `not_needed` (reason `restored_by_user`) until a person retries it; the savings statistics
subtract the conversion. A restore is refused when the original was purged or something else sits at its path, and a restart
in the middle of one finishes it, or forgets it when nothing had moved yet.

## API

All administrator only: `GET /transcode/summary` (counts per library, savings, health, originals kept, setup warnings),
`GET /transcode/items` (state, library, kind and path filters, cursor by id), `GET /transcode/items/{id}` (with the stored plan),
`POST /transcode/items/retry` (ids or a filter minus `excluded_ids`, optional `override`, `preview` only counts),
`POST /transcode/items/{id}/restore` (409 when the original is gone or its path is taken) and `POST /transcode/health/{kind}/resume`.

## Failures

Three errors on one file end it as `failed` until retried. Three consecutive failures of different files put that kind
into probing (one file far away at a time) and then backoff (5 minutes doubling to 6 hours), so a broken system does
not burn files into `failed`. A refused result is not an error for this purpose.

## After a crash

At the first dispatch after a start: a half-done replace is finished (the result is placed and recorded) or undone
(the original is restored from the bin), a running item with no job returns as interrupted, and work files no journal
names are deleted. The library ends with the original or the result known and present, never neither or both.

## Comics

A library whose media type is comics converts as kind `comic` through the same items, dispatcher, lanes (`audio`, as the
work is light), replace, recycle bin and restore. There is no ffmpeg run: `internal/platform/comicrepack` unpacks the
archive (cbz, cbr, cb7, cbt) into the work folder and writes a store-only cbz with the pages in natural order and
`ComicInfo.xml` kept; macOS and desktop litter is left out. What changes follows the target alone: a non-cbz container
becomes cbz; pages are re-encoded to `image_format` (webp or jpg) at `image_quality` when the file's commonest page
format differs; pages are downscaled to `page_long` x `page_short`, never upscaled, when the median page is more than
10 percent above. A page the encoder cannot make smaller keeps its bytes. A repack that changes only pages and saves
under 10 percent leaves the file alone (`saves_too_little`, not needed). The setup editor's video and audio switches
do not apply. A file that is not a comic archive (a PDF) is not needed; one with no pages or an unreadable archive is
blocked.

## Not built yet

The statistics page, file lists, retry and restore actions, and the settings pages come with the API and web steps.
