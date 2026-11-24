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

## Failures

Three errors on one file end it as `failed` until retried. Three consecutive failures of different files put that kind
into probing (one file far away at a time) and then backoff (5 minutes doubling to 6 hours), so a broken system does
not burn files into `failed`. A refused result is not an error for this purpose.

## After a crash

At the first dispatch after a start: a half-done replace is finished (the result is placed and recorded) or undone
(the original is restored from the bin), a running item with no job returns as interrupted, and work files no journal
names are deleted. The library ends with the original or the result known and present, never neither or both.

## Not built yet

The statistics page, file lists, retry and restore actions, and the settings pages come with the API and web steps.
