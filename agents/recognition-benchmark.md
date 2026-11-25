# Brief: benchmark Solarr's recognition on your hardware

You are an AI agent helping a person measure how long **Solarr** (https://github.com/makearr/Solarr) takes to scan and
recognise a media library on their hardware, and what it costs in CPU, memory and disk. The result is a report they can
post as a GitHub issue with their own account. The project has numbers from exactly one machine; yours make them mean something.

## Hard rules (do not relax them unless the person explicitly tells you to)

1. **Read-only.** Mount media with `:ro`, run with `SOLARR_MODE=read_only` and `SOLARR_SAFE_MODE=true`, add no indexer
   or download client, never write into their media folders.
2. **Throwaway.** A new, empty scratch folder for `/config` **on local disk** (not NFS/SMB: SQLite on a network
   filesystem measures the network, not Solarr). Remove the container at the end.
3. **No secrets, no names.** The report contains numbers and hardware, not file names, keys, tokens, IP addresses or host names.
4. **Be a good neighbour.** If the media sits on a NAS other people use, tell the person a scan reads every file header
   and may slow the NAS down for a while; let them pick the time.
5. **Do not post anything yourself** unless asked. Print the command; they run it.

## Step 1: ask the person

- Which folder(s) and media types to measure. A whole library gives the most useful number.
- Where the media lives: local SSD, local HDD, NAS over NFS, NAS over SMB, other.
- Whether keys may be used (TMDB, AcoustID). Keys change the timing (more providers asked); note it either way.
- How long they can let it run (a 200,000-file music library took roughly 1 to 3.5 hours on the developer's NAS).

## Step 2: record the hardware

Collect, without personal identifiers: CPU model and cores (`lscpu` or `nproc` + `/proc/cpuinfo`), RAM (`free -h`),
storage type and filesystem of the media and of the scratch folder (`df -T`, `findmnt`), network link to the NAS if any
(1 GbE, 2.5 GbE, ...), OS and kernel (`uname -r`), Docker version, and the Solarr version (bottom right of the web
interface).

Also the library size: file count and total size per media type
(`find <dir> -type f | wc -l`, `du -sh <dir>`), and how it is organised (tidy by an \*Arr, mixed, chaos).

## Step 3: start the throwaway and start the clock

```sh
SCRATCH=$(mktemp -d "$HOME/solarr-bench.XXXX"); mkdir -p "$SCRATCH/config"
docker run -d --name solarr-benchmark --init \
  -e PUID=$(id -u) -e PGID=$(id -g) -e SOLARR_MODE=read_only -e SOLARR_SAFE_MODE=true \
  -p 127.0.0.1:15456:5454 -v "$SCRATCH/config:/config" \
  -v /path/to/media:/media/music:ro \
  ghcr.io/makearr/solarr:latest
```

Create the account, the root folder and the library exactly as in [recognition-test.md](recognition-test.md), step 3.
**T0** is the moment the library is attached to its root folder (that starts the scan).

Sample resources once a minute until done, into a file:

```sh
while docker ps -q -f name=solarr-benchmark | grep -q .; do
  echo "$(date -Is) $(docker stats --no-stream --format '{{.CPUPerc}} {{.MemUsage}} {{.BlockIO}}' solarr-benchmark)"
  sleep 60
done >> "$SCRATCH/stats.log" &
```

## Step 4: the milestones

| Milestone | How to tell |
|---|---|
| **T1: scan done** | `GET /api/v1/scans` answers an empty list |
| **T2: settled** | `GET /api/v1/system/activity` stays empty for 5 minutes (identification, metadata, artwork done); take the time it first went empty |
| **Browse check** | at T2, time a library page and a search in the web interface (or `curl -w '%{time_total}'` on `GET /api/v1/works?category_id=C&limit=50`) |

Poll every one or two minutes; do not hammer the API. Then record the recognition figures (recognised, unverified,
unidentified) as in recognition-test.md step 5, without file names.

Optionally, measure a **second scan** of the same library (*Settings → Scheduler*, run the scan task, or restart the
container): a rescan should be much faster than the first, and that is worth knowing too.

## Step 5: write the report

`solarr-benchmark-report-YYYY-MM-DD.md`:

```markdown
## Benchmark: <type>, <N> files, <storage>

**Solarr version:** ...   **Keys:** TMDB yes/no, AcoustID yes/no
**Hardware:** CPU (cores), RAM, media on <storage, filesystem, link>, config on <local SSD/HDD>, OS/kernel, Docker
**Library:** <type>, <files>, <total size>, <organisation>

| Milestone | Time from T0 |
|---|---|
| Scan done (T1) | h:mm |
| Settled (T2) | h:mm |
| Files per minute (files / T1) | ... |

| Resource | Peak | Typical while scanning | At rest after T2 |
|---|---|---|---|
| CPU % | | | |
| Memory | | | |
| Disk read (BlockIO) | | | |

**Recognition:** recognised x %, unverified y, unidentified z
**Browsing at T2:** list page ... ms, search ... ms
**Observations:** what was slow, anything odd in Settings → Logs (message only)
```

Show it to the person, then print:

```sh
gh issue create --repo makearr/Solarr --title "Benchmark: <type>, <N> files, <storage>" --body-file solarr-benchmark-report-YYYY-MM-DD.md --label benchmark
```

(Drop `--label` if it does not exist; without `gh`, paste the file at https://github.com/makearr/Solarr/issues/new.)

## Step 6: clean up

Stop the stats loop, `docker rm -f solarr-benchmark`, confirm with `docker ps -a | grep solarr-benchmark` (no output),
and ask before deleting `$SCRATCH`.
