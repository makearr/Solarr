# Media tools in the image

ADR-30 keeps the Go binary pure and static and puts the two things with no good pure-Go
implementation into the **image** instead: acoustic fingerprinting and tag writing. They
are invoked as batched subprocesses through one `MediaTool` interface
(`internal/platform/mediatool`).

`ffmpeg` and `chromaprint` are in the Dockerfile's runtime stage (`fpcalc`/`ffprobe`); the
tagging helper is still not, per the still-undecided write path below. This page records
what was verified, so the choice started from measurements rather than from hoping the base
image could supply them — finding out later would have meant changing the base after it had
been published.

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

### kid3-cli works, and costs 542 MB

Verified against `alpine:3.24.1`, 2026-09-02. `kid3-cli` writes and reads back
`MUSICBRAINZ_TRACKID` on FLAC, MP3 **and M4A**, including the MP4 freeform atom ffmpeg
drops. Batched per-file reads work in one process, and absolute paths across different
directories work.

| Image | Size | Delta |
|---|---|---|
| `alpine:3` + ffmpeg | 200 MB | — |
| `+ chromaprint` (the floor: ADR-39 needs ffprobe, ADR-35 needs fpcalc) | 204 MB | +4 MB |
| `+ kid3` | 746 MB | **+542 MB, 127 packages** |
| `+ py3-mutagen` | 261 MB | +57 MB |

Alpine has no `kid3-cli` split package; `kid3` is the GUI build and the CLI ships with it,
Qt and all.

Three hazards, all workable, none obvious: it prints `Failed to create uridecodebin` and
friends **to stdout** rather than stderr unless gstreamer plugins are installed; it **exits
0 for a file that does not exist**, so success has to be read from the output; and `get all`
reports only the **basename**, so blocks must be matched to requested paths positionally.

### Reading needs no tagger at all

`ffprobe -print_format json -show_format` reads `MUSICBRAINZ_TRACKID` correctly on FLAC,
MP3 and M4A — the freeform atom included. Only *writing* it fails. Since ADR-29 takes
identity from embedded MBIDs, which is a read, **stage 3 needs no tagging helper**: the
`MediaTool` read path is ffprobe, already in the image at no extra cost, and the write path
waits for section 19.5's write-back at stage 5.

**The write-back tool is therefore still undecided**, deliberately, and the interface is
implemented without it. The options are mutagen and its Python, a pure-Go tagger per format
(ADR-30 explicitly allows satisfying the interface in-process where a solid library exists),
kid3 and its Qt, or linking taglib — which means CGO and is what ADR-30 exists to avoid.

## How the tools are called

`internal/platform/mediatool` is ADR-30's interface. `mediatool.Tool` rather than
`MediaTool`, because `mediatool.MediaTool` stutters.

**ffprobe takes one file per invocation**, so ADR-30's "one invocation per import
batch" cannot be met literally. What ADR-28 actually rules out — a synchronous
subprocess round trip on the caller's hot path — is met instead: the batch is one
call, and inside it invocations run with bounded concurrency, eight by default.

| | 200 files |
|---|---|
| Sequential | 8 s (40 ms each) |
| 8-way | 1 s (5 ms each) |

At ADR-22's 500k that is about 42 minutes of background scanning, against 5.5 hours
sequential. A pure-Go tag reader would avoid the process spawns, but **ADR-39 needs
ffprobe on every file regardless** — a release name states claims, a probe states
facts — so reading tags from the same invocation is free and a second pass would
not be.

**One unreadable file does not fail a batch.** At 500k files there is always one, so
each result carries its own error and the batch returns. Results are matched to
requests **positionally**, never by the filename a tool echoes back.

## Running outside Docker

`ffprobe`, `fpcalc` and (from stage 5) the tagging helper must be on `PATH`. Startup logs
one warning naming every missing tool rather than each file failing obscurely at scan time,
and a missing tool degrades: the import proceeds, and tag write-back is recorded as deferred
rather than dropped.
