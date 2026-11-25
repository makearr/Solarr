# Preview player

> **Level: 🟢 Beginner.** Press play on anything. Yes, even that weird MKV.

## Feature set

- **A play button beside every track, episode and film.**
- **Lives in the page frame**, so it keeps playing while you browse: as a **bar**, a **corner window** or **full screen**.
- **Direct when possible, converted when not.** Files your browser can play are streamed as they are; the rest are
  converted on the fly by the ffmpeg in the image.
- **Quality menu** (original, 1080p, 720p, 480p) with the source bitrate shown.
- **GPU transcoding** on Intel and AMD (VAAPI, Quick Sync) when you pass `/dev/dri` into the container; NVIDIA (NVENC)
  with the NVIDIA container runtime.

## Benefits

- **Check a file without leaving Solarr.** Is this the right episode? Is the audio in sync? Is that "remaster" any good?
- **No media server needed** for a quick listen or look.
- **It never competes with your library work**: a conversion only runs while someone is watching.

## Technical details

- **One ffmpeg per stream**, started on demand and stopped when the player closes.
- **On the GPU, frames stay on the GPU** where it can decode the source (VAAPI and NVENC); otherwise it decodes in
  software and encodes on the GPU.
- **Library transcoding steps aside:** while the player converts, the background [transcoding](transcoding.md) lanes
  shrink and surplus ffmpeg processes are paused.
- **Access control applies to playback** like to everything else: a file plays only through a library the account may read.
- **The NVIDIA path is untested** (no NVIDIA hardware in the project yet). [Help wanted](../../README.md#how-you-can-help)!

Further reading: [media player](../media-player.md) (settings, engines, devices), [container runtime](../container-runtime.md).
