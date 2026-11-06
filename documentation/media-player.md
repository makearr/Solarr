# Built-in media player

> **Level: 🟡 Advanced.** The preview player.

Previews an audio or video file from the web interface, in the browser, without a download.

## Where it appears

A play button sits beside every file that is audio or video: a track, an episode, a movie file. The player lives in
the page frame, so it keeps playing while you browse, and it has three placements (the choice is kept per account,
as the `player.mode` preference):

| Mode | Where |
|---|---|
| Bar | a strip under the page, above the status bar |
| Small window | a floating window in the lower right corner |
| Full screen | covers the page; Escape returns to the bar |

Previous and next walk the queue; a finished file starts the next.

## How a file reaches the browser

`GET /files/{id}/playback` says what the file is and whether a browser is likely to play it as it is
(`direct_play_likely`, from the extension and the measured codecs). The player then picks a route:

- **Direct**: `GET /files/{id}/stream` serves the bytes with byte ranges (`http.ServeContent`), so seeking costs
  nothing. Used for MP3, FLAC, Ogg, Opus, AAC/M4A, WAV and H.264/VP9/AV1 in MP4 or WebM.
- **Converted**: `GET /files/{id}/transcode?start=SECONDS` runs ffmpeg and streams its output as it is made: fragmented
  MP4 (H.264 + AAC) for video, MP3 for audio. Used when the file is unlikely to play, and as the fallback when the
  browser rejects a direct file. A converted stream has no length of its own, so a seek asks again from the new
  second; the length shown comes from the probe.

Both routes check the caller can read the file's root folder (ADR-27); anything else is a 404.

## Quality

The player shows the original file's size, codec and measured bitrate, and video has a quality menu: **Original**
(the default), 1080p, 720p, 480p. Original plays the file as it is, or converted at its own size and bitrate when the
browser cannot play it. The others are always conversions (`?quality=`), 8000, 4000 and 1800 kbit/s, never above the
source bitrate and never taller than the configured cap. Changing quality mid-stream resumes at the same second.

## Hardware encoding

Encoding runs on the GPU, and where the GPU can decode the source (VAAPI and NVENC) the frames never leave it: the
whole path is GPU (`scale_vaapi`, `scale_cuda`). A source the GPU cannot decode (an unsupported codec or bit depth)
fails before the first byte, and the request is retried one step down: GPU encoding with software decoding, then the
processor alone. On the Intel test machine the full-GPU path used about a fifth of the processor time of the
software-decode one. The NVIDIA path is untested (no NVIDIA hardware here).

At start Solarr tries each encoder once by encoding a single black frame, and keeps what worked
(`internal/platform/transcode`, `Detect`). A device node alone proves nothing: a missing driver or a container without
the render group fails the try and the reason is shown on Settings → Player.

| Encoder | Hardware | Needs |
|---|---|---|
| `nvenc` | NVIDIA | the NVIDIA container runtime |
| `vaapi` | Intel and AMD | `/dev/dri/renderD*`, `intel-media-driver` or `mesa-va-gallium` (both in the image) |
| `qsv` | Intel | as VAAPI, plus the oneVPL runtime |

`auto` takes the first working one in that order. Settings → Player can force an encoder, name the render device
or turn hardware off; a forced encoder that never worked falls back to the processor and says so. 

### Giving the container a GPU

```yaml
services:
  solarr:
    devices:
      - /dev/dri:/dev/dri
    group_add:
      - "991"   # the host's render group: getent group render
```

## Settings (Settings → Player)

| Key | Default | |
|---|---|---|
| `streaming.transcode_enabled` | true | off: only files the browser plays as they are |
| `streaming.hardware` | auto | auto, software, vaapi, qsv, nvenc |
| `streaming.device` | empty | render node; empty takes the detected one |
| `streaming.max_height` | 0 | taller video is scaled down, whatever quality is picked; 0 sets no cap |
| `streaming.video_kbps` | 6000 | bitrate of a converted original whose own is unknown |
| `streaming.max_sessions` | 2 | conversions at once; a further one gets 429 |

Not yet in the portable settings export.

The player has a mute button and a volume slider; the volume is remembered in the browser.
