# The Docker image

> **Level: 🟡 Advanced.** Installing in detail: the image, mounts, memory, and running parts of Solarr from RAM.

One image, **amd64 and arm64**, from Docker Hub (`makearr/solarr`) and GitHub (`ghcr.io/makearr/solarr`).
The short version is in the [README](../README.md#quick-start); the 15-minute walk-through in [getting started](getting-started.md).

## Docker run

```sh
docker run -d --name solarr \
  --restart unless-stopped \
  -e PUID=1000 -e PGID=1000 -e TZ=Europe/Berlin \
  -p 5454:5454 \
  -v /srv/solarr/config:/config \
  -v /srv/media:/media \
  -v /srv/downloads:/downloads \
  makearr/solarr:latest
```

A commented [docker-compose.yml](../docker-compose.yml) is in the repository.

## Mounts and permissions

- **`PUID`/`PGID`** set who Solarr runs as; it takes ownership of its own folders only, never your media. Do not set `user:`.
- **Keep `/config` on local storage.** SQLite is not reliable over NFS/SMB.
- **Hardlinks need one filesystem**: put the library and completed downloads on the same mount, or Solarr copies instead.
- **`/downloads` must be the same path your download client reports**, or set a path mapping on the client connection.
- **Mount your library `:ro`** while you are trying Solarr out, and/or start in `SOLARR_MODE=read_only`.
- **GPU:** pass `/dev/dri` for Intel/AMD, or use the NVIDIA container runtime, for the player and transcoding.
- Port 5454 can be changed with `SOLARR_PORT`.

More in [container runtime](container-runtime.md) and [startup checks](startup-checks.md).

## Size, and keeping it lean

| | Measured |
|---|---|
| Download from Docker Hub (compressed, amd64 / arm64) | about **84 MB / 81 MB** (first release) |
| On disk after pulling | about **187 MB** |
| The Solarr binary itself, web interface included | about **36 MB** |

The Go binary is static (`CGO_ENABLED=0`) with the interface embedded: no .NET runtime, no Node, no database server, no
message broker. The rest is a small Alpine base plus the few tools Solarr needs: `ffprobe`/`ffmpeg` (measuring files,
the player, transcoding), `fpcalc` (audio fingerprints) and a tag writer. GPU drivers for Intel and AMD are included and
cost nothing unless you pass a device in. Each addition is weighed against its size (the tag writer was accepted at
+57 MB and is noted as such in the Dockerfile). Details: [media tools](media-tools.md).

## Memory

| Library | RAM to plan for |
|---|---|
| **Small** (a few thousand items) | **256 MB** is comfortable. A fresh instance idles at about **45 MB** resident (measured) |
| **Large** (250,000+ files) | **1 GB** is a sensible budget. The database page caches have a designed ceiling of **384 MiB** in total (default), enforced by a test; the rest is Go working memory |

The idle figure is measured; the large-library figure is the designed ceiling plus headroom, not yet a measurement on a
full 250,000-file library. Treat it as a budget, and [tell us what you see](../README.md#how-you-can-help).

**More RAM for the cache.** *Settings → Cache* sets one ceiling for the page caches of all four databases
(`database.cache_size_kb`, default 384 MiB, at least 128 MiB; the library database takes 80 %). On a machine with RAM to
spare, raise it until most of a large library is served from memory; it applies at the next start. The page shows each
database's size, cache use and hit ratio, and a **Shrink** button drops pictures and provider answers unused for a week.

**Database and cache on tmpfs (expert, off by default).** Two options run a working copy from RAM and write it back on
an interval: the **cache** (every 10 minutes) and the **four databases** (every 5 minutes, only those that changed). The
RAM directory is `/dev/shm/solarr`; its size is Docker's `shm_size`:

```yaml
    shm_size: 8gb
    stop_grace_period: 2m
```

Solarr turns an option on only when the directory is at least 20 % larger than what it would hold and the container's
memory allows it, and only with "I understand the risk" ticked. **A crash, kill or power cut loses what changed since the
last copy**; backups are unaffected. If free memory falls below a twentieth of the limit, Solarr copies to disk, switches
the cache (then the databases) off and restarts from disk. Details: [RAM copies](memory-copies.md).

## Environment variables

**None is required.** A variable that is set wins over `config.yml` and over a value saved in the web interface. Append
`_FILE` to any `SOLARR_*` variable to read it from a file (Docker secrets). Any `config.yml` key is `SOLARR_` plus its
path in upper case with dots as underscores. The full list, with defaults and precedence:
[environment variables](environment-variables.md). `solarr config check` validates the file and the environment without
starting the server.

The ones people actually set:

| Variable | Default | Meaning |
|---|---|---|
| `PUID` / `PGID` | `1000` | User and group the process runs as. `0` is refused |
| `TZ` | `UTC` | Time zone for air dates and log timestamps |
| `SOLARR_BASE_URL` | `/` | Path prefix behind a reverse proxy, e.g. `/solarr` |
| `SOLARR_MODE` | `normal` | `normal`, `read_only`, `pause_downloads`, `observe` or `arr_companion` ([Solarr mode](solarr-mode.md)) |
| `SOLARR_SAFE_MODE` | `false` | Keeps transcoding, automatic search, RSS, cutoff and \*Arr push off ([safe mode](safe-mode.md)) |
| `SOLARR_TMDB_API_KEY` | unset | Your TMDB key; TV and movies work on less data without it |
| `SOLARR_ACOUSTID_API_KEY` | unset | Your AcoustID key, for files with no tags at all |
| `SOLARR_DATABASE_CACHE_SIZE_KB` | `393216` | One ceiling for the database page caches; raise it for a large library |
