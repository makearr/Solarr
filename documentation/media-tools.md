# Media tools in the image

ADR-30 keeps the Go binary pure and static and puts the two things with no good pure-Go
implementation into the **image** instead: acoustic fingerprinting and tag writing. They
are invoked as batched subprocesses through one `MediaTool` interface, which lands with
stage 3.

Nothing here is in the Dockerfile yet. An image should not carry tools nothing calls. This
page records what was verified, so stage 3 starts from measurements rather than from
hoping the base image can supply them — finding out later would mean changing the base
after it has been published.

## What Alpine has, verified against `alpine:3.24.1`

| Tool | Package | Version | `x86_64` | `aarch64` |
|---|---|---|---|---|
| `fpcalc` | `chromaprint` | 1.6.0-r0 | yes | yes |
| `ffprobe`, `ffmpeg` | `ffmpeg` | 8.1.2-r0 | yes | yes |

Both architectures carry identical versions, which matters because releases are multi-arch
(`linux/amd64,linux/arm64`). Checked against the community `APKINDEX` for both, not just
the one this machine runs.

`fpcalc` was run against a generated tone and returned a fingerprint and a duration.
`ffprobe` reports the tags a file carries, which is ADR-39's "facts" side.

## What they cost

| Image | Size |
|---|---|
| `alpine:3` | 8.9 MB |
| `+ ffmpeg` | 141.4 MB |
| `+ ffmpeg + chromaprint` | 143.5 MB |

The bulk is ffmpeg's libraries, and **ADR-39 needs `ffprobe` regardless** — a release name
states claims, a probe states facts. Against that, fingerprinting costs 2 MB. Installing
`chromaprint` alone still pulls the same libraries, so there is no cheaper ordering.

## Tag writing: ffmpeg is not enough

ffmpeg can write tags, and for two of the three formats ADR-30 names it is correct. It is
not a substitute for a tagging helper:

| Format | Custom tag (`MUSICBRAINZ_TRACKID`) |
|---|---|
| FLAC (Vorbis comments) | written and read back |
| MP3 (ID3v2) | written and read back |
| M4A (MP4) | **silently dropped** |

MP4 keeps custom tags in a freeform atom (`----:com.apple.iTunes:...`) and ffmpeg does not
write one for an arbitrary `-metadata` key. It does not fail; the tag is simply absent
afterwards. ADR-29 takes identity from embedded MBIDs, so a format that quietly loses them
on write-back is a correctness problem, not a cosmetic one.

Separately, **ffmpeg rewrites the container rather than editing tags in place.** Section
19.5's write-back is "fill gaps only, never overwrite", and rewriting every file to add one
missing tag is a different operation with different risks at 500k files.

`py3-mutagen` (1.47.0, in Alpine) does both correctly: it wrote the MP4 freeform atom and
read it back, and the file grew by about a kilobyte rather than being re-encoded. It costs
41 MB on top, because it brings Python.

**This is a stage-3 decision and is not taken here.** The options are mutagen and its
Python, a pure-Go tagger per format (ADR-30 explicitly allows satisfying the interface
in-process where a solid library exists), or linking taglib, which means CGO and is what
ADR-30 exists to avoid.

## Running outside Docker

`fpcalc` and the tagging helper must be on `PATH`. `/api/v1/ready` reports which are
missing rather than failing obscurely at import time, and a missing tool degrades: the
import proceeds, and tag write-back is recorded as deferred rather than dropped.
