# Environment variables

> **Level: 🟡 Advanced.** Configure the container without editing files.

Every variable Solarr reads. **None is required**: with nothing set, the image starts on port 5454 with
everything under `/config`. In Docker, a variable is how you configure a container without editing files.

## Precedence

A variable that is set **wins**; one that is unset changes nothing.

| Where the setting lives | What a set variable does |
|---|---|
| `config.yml` (bootstrap settings) | Overrides the file value for this run. The file is not rewritten |
| `settings.db` (API keys, root folders, network) | Is written into the database at every start, replacing what was stored |
| Not set | The value in `config.yml` / `settings.db` (set from the web interface) stays |

Because `settings.db` is rewritten, an API key or proxy changed in the web interface is **put back** at the next
restart while its variable is still set. Remove the variable to manage that setting from the interface again.
An empty variable counts as unset.

A value that fails validation (a port of `99999`, a relative path, an unknown level) refuses startup and names the
variable. Secret values are never printed.

## Reading a value from a file (Docker secrets)

Append `_FILE` to any `SOLARR_*` variable and give it a path: the value is read from that file, minus one
trailing newline. Setting both forms of one variable is an error.

```yaml
environment:
  SOLARR_TMDB_API_KEY_FILE: /run/secrets/tmdb_key
```

## Container

| Variable | Default | Meaning |
|---|---|---|
| `PUID` / `PGID` | `1000` | User and group the process runs as and the owner of everything it writes. `0` is refused. See [container-runtime.md](container-runtime.md) |
| `TZ` | `UTC` | Timezone for air dates and log timestamps. An unknown zone falls back to UTC with a warning |
| `SOLARR_HEALTHCHECK_DNS` | unset | A host name the image healthcheck must also resolve, for instances behind a VPN container |

## Location and network

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `SOLARR_CONFIG_DIR` | `paths.config_dir` | `/config` | Directory holding `config.yml`; the file is looked up here unless `--config` is given. Mount a volume at the same path. `db_dir`, `cache_dir`, `mediacover_dir` and `backup_dir` do **not** follow it: set them as well if they should move |
| `SOLARR_DB_DIR` | `paths.db_dir` | `/config/db` | Where `library.db`, `settings.db`, `jobs.db` and `statistics.db` live. Local disk only, never a network filesystem |
| `SOLARR_CACHE_DIR` | `paths.cache_dir` | `/config/cache` | Disposable cache: pictures of items not in the library yet, provider responses, the transcode scratch folder (`transcode/`, emptied at start) |
| `SOLARR_MEDIACOVER_DIR` | `paths.mediacover_dir` | `/config/mediacover` | Pictures of items in the library. A picture moves here from the cache when its item is added, without asking the provider again; the cache budget never touches it |
| `SOLARR_BACKUP_DIR` | `paths.backup_dir` | `/config/backups` | Database backups |

`paths.data_dir` is ignored. An older `config.yml` that names `/config/data/db`, `/config/data/cache` or `/config/data/backups` is moved to the new places at the first start, and the file is rewritten; a path set to anything else is left alone, and a start where both the old and the new folder exist is refused.
| `SOLARR_PORT` | `server.port` | `5454` | HTTP port. Always wins over the Network screen, and is written to it so the screen shows it in effect |
| `SOLARR_BIND` | `server.bind` | `0.0.0.0` | Address to listen on |
| `SOLARR_BASE_URL` | `server.base_url` | `/` | Path prefix when served behind a reverse proxy under a sub-path, e.g. `/solarr` |
| `SOLARR_PROXY_URL` | `network.proxy` (`settings.db`) | unset | Outbound proxy for metadata traffic: `http://`, `https://` or `socks5://` with an explicit port. Turns the proxy on |

## Media folders

| Variable | Default | Meaning |
|---|---|---|
| `SOLARR_ROOT_FOLDERS` | unset | Comma-separated absolute paths, each optionally labelled: `/media/music,/media/tv` or `Music=/media/music,TV=/media/tv` |

Folders not yet known are added as root folders at startup. Folders are **never removed**: a root folder owns
library history, so dropping a path from the variable leaves it in place (remove it in the interface). A path that is
not absolute refuses startup. Existence is not checked at this point; an unmounted folder is handled as one added from the interface.
Which media type a folder holds is still set in the interface.

## API keys

Each key is stored sealed in `settings.db`. Unset means the key stored from the interface is used (or none).

| Variable | Provider | Without a key |
|---|---|---|
| `SOLARR_TMDB_API_KEY` | TMDB (v3 key or v4 token) | TV and movies work on less data |
| `SOLARR_TVDB_API_KEY` | TheTVDB project key | The built-in project key is used |
| `SOLARR_FANART_API_KEY` | fanart.tv | No fanart |
| `SOLARR_OMDB_API_KEY` | OMDb | No OMDb ratings |
| `SOLARR_MDBLIST_API_KEY` | MDBList | No MDBList ratings |
| `SOLARR_ACOUSTID_API_KEY` | AcoustID | Music is identified without the fingerprint step |
| `SOLARR_THEAUDIODB_API_KEY` | TheAudioDB | The public key is used |
| `SOLARR_GOOGLEBOOKS_API_KEY` | Google Books | Shared quota; setting one switches the provider on |
| `SOLARR_HARDCOVER_API_KEY` | Hardcover | Provider unavailable |
| `SOLARR_STASHDB_API_KEY` | StashDB | Provider unavailable |
| `SOLARR_THEPORNDB_API_KEY` | ThePornDB | Provider unavailable |
| `SOLARR_FANSDB_API_KEY` | FansDB | Provider unavailable |

Indexer and download-client credentials belong to individual entries and are set in the interface.

## Security

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `SOLARR_ENCRYPTION_KEY` | `security.encryption_key` | generated on first run | `base64:` plus 32 random bytes, e.g. `base64:$(openssl rand -base64 32)`. Encrypts the credentials in `settings.db`. **Losing it means re-entering every credential**: if you supply it by variable, keep supplying the same one |
| `SOLARR_AUTH_MODE` | `security.auth_mode` | `local` | `local`, `proxy`, `apikey_only` or `none`. `none` serves everyone as administrator |
| `SOLARR_SECURITY_TRUSTED_PROXY_HEADER` | `security.trusted_proxy_header` | `X-Remote-User` | Header carrying the user name when `auth_mode` is `proxy` |
| `SOLARR_SECURITY_TRUSTED_PROXY_CIDRS` | `security.trusted_proxy_cidrs` | empty | Proxies allowed to set that header, as a list: `[10.0.0.0/8, 172.16.0.0/12]` |

## Logging and mode

| Variable | Config key | Default | Meaning |
|---|---|---|---|
| `SOLARR_LOG_LEVEL` | `logging.level` | `info` | `debug`, `info`, `warn` or `error` |
| `SOLARR_LOG_FORMAT` | `logging.format` | `json` | `json` or `text` |
| `SOLARR_MODE` | `mode` | `normal` | `normal`, `read_only` (no writes into a library), `pause_downloads`, `observe` (read-only, no searches, no downloads) or `arr_companion` (observe, plus monitoring sent back to the *Arrs). Sets Settings, General, Solarr mode at every start |

## Every other `config.yml` setting

Any other key is `SOLARR_` plus its path in upper case, dots replaced by underscores: `database.busy_timeout` is
`SOLARR_DATABASE_BUSY_TIMEOUT`. Durations are `15s`, `500ms`, `90d`; sizes are `50GB`, `512MB` or a byte count (decimal units).

| Variable | Default | Meaning |
|---|---|---|
| `SOLARR_DATABASE_BUSY_TIMEOUT` | `15s` | How long a database waits on a lock |
| `SOLARR_DATABASE_PAGE_SIZE` | `4096` | SQLite page size; applied only when a database is created |
| `SOLARR_DATABASE_CACHE_SIZE_KB` | `393216` | One ceiling for the page caches of all databases and connections together |
| `SOLARR_DATABASE_WRITE_WARN` | `50ms` | A write transaction longer than this is logged |
| `SOLARR_DATABASE_WRITE_HARD` | `2s` | A write transaction longer than this is a fault |
| `SOLARR_BACKUP_SETTINGS_ON_CHANGE` | `true` | Snapshot `settings.db` on every change |
| `SOLARR_BACKUP_KEEP_SETTINGS` | `20` | Settings snapshots kept |
| `SOLARR_BACKUP_BEFORE_MIGRATION` | `true` | Back up before a schema migration. Cannot be turned off |
| `SOLARR_RETENTION_DECISIONS_FULL` | `90d` | How long full decision detail is kept before it is rolled up |
| `SOLARR_RETENTION_CANDIDATES` | `7d` | How long search candidates are kept |
| `SOLARR_STORAGE_MIN_FREE_BYTES` | `50GB` | Downloads stop when free space falls below this |
| `SOLARR_STORAGE_MIN_FREE_PERCENT` | `2` | ...or below this percentage |
| `SOLARR_WORKERS_SEARCH` | `4` | Concurrent searches |
| `SOLARR_WORKERS_ACQUIRE` | `2` | Concurrent downloads started |
| `SOLARR_WORKERS_METADATA` | `4` | Concurrent metadata lookups |
| `SOLARR_NETWORK_PUBLIC_IP_CHECK_ENABLED` | `false` | Periodically ask a third party which address this host connects from |
| `SOLARR_NETWORK_PUBLIC_IP_CHECK_ENDPOINT` | `https://api.ipify.org` | Service for that check |
| `SOLARR_NETWORK_PUBLIC_IP_CHECK_INTERVAL` | `6h` | How often |
| `SOLARR_METADATA_MUSICBRAINZ_BASE_URL` | `https://musicbrainz.org/ws/2` | Direct MusicBrainz, or a local mirror |
| `SOLARR_METADATA_MUSICBRAINZ_BRAINZMASH_ENABLED` | `true` | Try the BrainzMash mirror pool first |
| `SOLARR_METADATA_MUSICBRAINZ_BRAINZMASH_BASE_URL` | `https://api.brainzmash.cc/ws/2` | Its address |
| `SOLARR_METADATA_MUSICBRAINZ_PROXY_ENABLED` | `true` | Try the Servarr metadata proxy next |
| `SOLARR_METADATA_MUSICBRAINZ_PROXY_BASE_URL` | `https://api.lidarr.audio/api/v0.4` | Its address |
| `SOLARR_METADATA_MUSICBRAINZ_REFRESH_AFTER` | `90d` | Age at which a cached answer is fetched again |

`solarr config check` validates the file **and** the environment without starting the server.

## Example

```yaml
services:
  solarr:
    image: makearr/solarr:latest
    environment:
      PUID: 1000
      PGID: 1000
      TZ: Europe/Berlin
      SOLARR_PORT: 5454
      SOLARR_LOG_LEVEL: info
      SOLARR_ROOT_FOLDERS: Music=/media/music,TV=/media/tv,Movies=/media/movies
      SOLARR_TMDB_API_KEY_FILE: /run/secrets/tmdb
      SOLARR_FANART_API_KEY: ${FANART_KEY}
    secrets: [tmdb]
```
