# Container runtime

How the image decides who it runs as, what it owns, and what it refuses to touch.

## PUID and PGID

`PUID` and `PGID` govern **both** the user the process becomes and the ownership of everything
it writes. `1000:1000` is not a policy — it is only what happens when neither is set.

They are deliberately **not** declared as `ENV` defaults in the image. The default lives in
`internal/platform/privilege` alone, so a variable being present means the operator set it. That
distinction is what lets a request that cannot be honoured be reported as one.

## Startup sequence

1. Resolve `PUID`/`PGID`. An unparseable value or `0` is refused: a typo would otherwise run as
   1000 and surface much later as a permission error pointing at the volume.
2. Report immediately if the identity cannot be reached (see below).
3. Load or create `config.yml`.
4. Take ownership of the managed directories, then drop privileges permanently.
5. Run the section 14.3 directory checks — as the dropped user, so they answer the question
   that matters.

The order matters. The drop sits after loading configuration because the directories to own are
named in it, and before the directory checks because those ask whether *this user* can write.
Running them as root answers yes and the application then fails later as somebody else.

## What is owned, and what is not

Owned: `config_dir`, `data_dir`, `db_dir`, `cache_dir`, `backup_dir` — the directories
Solarr creates.

Never owned: the media roots. They hold the 500k files of ADR-22, they belong to the operator,
and rewriting their ownership on every boot would be slow and presumptuous.

Ownership is re-applied on every start rather than only when the top directory looks wrong. A
directory's owner says nothing about its contents: a first run that failed after writing
`config.yml` leaves `/config` owned correctly and the file inside owned by root, and the
shortcut would hand the process a configuration it cannot write.

## Permissions outside `/config`

Reported, never corrected. A library root this process cannot write is stored, flagged
`writable: false` so the interface can show it, and named in a startup warning with its path,
owner, mode and the process identity. Startup continues.

Refusing would mean a library owned by another user could not be configured at all. A path that
is relative, absent or a file is still refused — that is a mistake in what was typed, not a
decision about ownership.

## Capabilities

None need adding. Docker's default set already contains `CHOWN`, `DAC_OVERRIDE`, `SETUID` and
`SETGID`, which is everything the startup sequence uses.

`cap_drop: ALL` still works with those four added back.

## `--user`

The one thing that defeats `PUID`/`PGID`. Changing uid needs root, and a container handed to us
as somebody else cannot reach the requested identity at all.

When `PUID`/`PGID` were set and disagree with the current identity, that is reported **before
anything touches the filesystem**. Otherwise the first symptom is a permission error while
creating `config.yml`, which sends the operator to inspect a volume when the answer is a flag
they passed.

## Network port

The default port is **5454** (`EXPOSE 5454`, `docker-compose.yml` maps `5454:5454`). It replaced
8080 on 2026-09-10 (ADR-58) — a one-line change to any existing compose file or bookmark.

Resolution order, applied at startup and reported in the startup banner:

1. `SOLARR_SERVER_PORT` — always wins. This is the recovery path out of a bad setting
   written from the interface: if a value written from the Network screen locks the port to
   something unreachable, set this and restart.
2. `settings.db`'s own policy — what the Network screen writes. Changing it there needs a
   restart before it takes effect; the screen says so.
3. `config.yml`'s `server.port` — the bootstrap value, and the fallback for as long as nothing
   has been written from the screen yet.
4. `5454` — the default if none of the above set anything.

The image's own `HEALTHCHECK` (`solarr healthcheck`) re-derives this same order from
`config.yml` and `settings.db` independently on every run, so a port changed from the interface
does not make the container report itself unhealthy.

## Network egress and the IP allowlist

`network.proxy`/`network.proxy_bypass` (a global HTTP or SOCKS5 proxy for outbound
metadata traffic; Solarr has no update check) and `network.allow_cidrs` (an IP allowlist) are both `settings.db` policy,
edited from the Network screen, and apply live with no restart. The allowlist can only ever
**deny** — it never authenticates and never grants (Invariant 89) — and a change that would lock
out the address making it is refused outright. `/api/v1/health` is always exempt, so a container
healthcheck from Docker's own bridge network is never affected by a LAN-only rule.

## TZ

Sets the container's timezone. Air dates are timezone-dependent (architecture section 19.2) and
log timestamps are read by people, so an unset `TZ` leaving everything in UTC is a recurring
support question in this ecosystem.

Applied by `internal/platform/privilege` before anything logs. An unknown zone is reported and UTC is
kept, rather than failing to start over a display preference.
