# Duplicate videos

Finding the same video stored more than once, in another encode, size or container (ADR-35).

## Hashing

The probe sweep's last pass hashes every video: seven frames evenly across its length, one ffmpeg call each, each frame
reduced to 32x32 grey and hashed by a DCT into 64 bits. The mean image's hash only finds candidates; the per-frame
hashes decide (`database-layout.md`, `video_frame_hash`).

The pass is a setting, `scan.video_phash` (default on), edited by an administrator under Storage > Duplicates
(`GET/PUT /settings/video-hashing`). Off, nothing new is hashed; hashes already stored still serve the finder, and the
report says so (`hashing_enabled`).

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
The per-frame bound is a quarter wider (`frameBitsFor`), and two thirds of at least four shared frames must still match.

## Finding and resolving

- One library: `GET /categories/{id}/similar-videos`, on the library's Duplicates page; resolving needs manage access.
- Every library: `GET /duplicates/similar-videos`, Storage > Duplicates, administrators only.
- `POST .../duplicates/resolve` (one library) and `POST /duplicates/similar-videos/resolve` (all) keep the chosen file
  and, per other file, either merge it into the kept one (every path becomes a link; the old file waits in the recycle
  bin) or recycle it. A merge across libraries is refused: a merged file would belong to two libraries.

A file linked at several paths (one media object, many paths) is one video in a group with a `path_count`, not several copies; a group needs two different files. Found live on the docu library, where 42 of 238 groups were one file listed repeatedly.
