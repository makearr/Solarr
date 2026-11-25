# Brief: test Solarr's recognition on a real library

You are an AI agent helping a person test **Solarr** (https://github.com/makearr/Solarr), a self-hosted media manager.
Your job: run a **throwaway** Solarr against their media library, **read-only**, measure how much of it Solarr
recognises, collect the files it got wrong, and write a report the person can post as a GitHub issue with their own
account. Optionally, also test importing from their Lidarr/Sonarr/Radarr/Readarr.

## Hard rules (do not relax them unless the person explicitly tells you to)

1. **Read-only.** Mount every media folder with `:ro`. Run Solarr with `SOLARR_MODE=read_only` and
   `SOLARR_SAFE_MODE=true`. Never add an indexer or a download client. Never move, rename, tag, link or delete a file
   of theirs, and never write into their media folders.
2. **Throwaway.** Use a new, empty scratch folder for `/config`. Never reuse the config folder of a Solarr they already
   run. Remove the container and (after asking) the scratch folder at the end.
3. **No secrets in the report.** No API keys, tokens, passwords, session cookies, the encryption key, IP addresses, host
   names or user names. If you need a key (TMDB, AcoustID) ask for it, use it only in the throwaway, and never write it
   into the report or any file outside the scratch folder.
4. **Ask before publishing names.** File and folder names reveal what someone collects. Ask whether the report may
   contain real file names, or whether you should anonymise them (keep the *shape* of the name, replace titles with
   placeholders such as `Artist A - Album B (1999)/03 - Track C.flac`). Default: anonymise.
5. **The \*Arrs are read-only too.** If you test the \*Arr import, leave "send monitoring back" off. Solarr only sends GET
   requests to an \*Arr unless that switch is on.
6. **Do not post anything yourself** unless the person asks you to. Print the command; they run it.

## Step 1: ask the person

Ask, in one go:
- Which media folder(s) to test, and what each holds (music, TV, anime, movies, books, audiobooks, comics). A subset
  (a few hundred to a few thousand files) is fine and faster; a whole library is more useful.
- Real file names in the report, or anonymised?
- Optional keys for the throwaway: TMDB (better TV/movie data), AcoustID (fingerprints for untagged music). Without them
  the test still runs.
- Whether to also test the \*Arr import, and if so the \*Arr's address and API key.
- Docker available? (Required. The image is `ghcr.io/makearr/solarr:latest`, amd64 and arm64.)

## Step 2: start the throwaway

```sh
SCRATCH=$(mktemp -d /tmp/solarr-test.XXXX); mkdir -p "$SCRATCH/config"
docker run -d --name solarr-recognition-test --init \
  -e PUID=$(id -u) -e PGID=$(id -g) \
  -e SOLARR_MODE=read_only -e SOLARR_SAFE_MODE=true \
  -p 127.0.0.1:15455:5454 \
  -v "$SCRATCH/config:/config" \
  -v /path/to/their/music:/media/music:ro \
  ghcr.io/makearr/solarr:latest
curl -s http://127.0.0.1:15455/api/v1/health    # {"status":"ok"} when ready
```

Add one `-v ...:/media/<type>:ro` per folder they named. Record `docker inspect --format '{{.Config.Image}}'` and the
version shown at the bottom right of the web interface (or `GET /api/v1/about`) for the report.

## Step 3: create an account and the libraries

The API lives under `/api/v1` and takes a session cookie. A cookie jar keeps it.

```sh
A=http://127.0.0.1:15455/api/v1; J="$SCRATCH/cookies"
curl -s -c $J -H 'Content-Type: application/json' \
  -d '{"username":"tester","password":"a-long-throwaway-password"}' $A/setup/admin
# one root folder + one library per media folder; media_type: music, tv, movie, book, audiobook, comic
curl -s -b $J -H 'Content-Type: application/json' -d '{"path":"/media/music"}' $A/rootfolders          # -> {"id": R, ...}
curl -s -b $J -H 'Content-Type: application/json' -d '{"name":"Music","media_type":"music"}' $A/categories   # -> {"id": C, ...}
curl -s -b $J -H 'Content-Type: application/json' -d '{"root_folder_id": R}' $A/categories/C/rootfolders   # starts the scan
```

For TV, `"kind":"anime"` makes an anime library. Keys go in with
`curl -s -b $J -X PUT -H 'Content-Type: application/json' -d '{"api_key":"..."}' $A/metadata/providers/tmdb/apikey`
(`acoustid` for AcoustID). Alternatively do all of this in the web interface at http://127.0.0.1:15455 (setup wizard,
then *Settings → Libraries*). If an endpoint answers 404, the API has moved on: use the web interface and say so in the report.

## Step 4: wait for the scan, then let metadata settle

- `GET $A/scans` lists running scans with `files_seen`; an empty list means scanning is done.
- Then wait until `GET $A/system/activity` (running jobs) stays empty for about five minutes: identification and
  metadata continue after the scan. Large libraries take hours; poll every few minutes, do not hammer.

## Step 5: measure

Per library, record:
- **Files seen** (scan) and **works recognised** (`GET $A/works?category_id=C&limit=100`, follow `next_cursor`; or the
  count in the library's sidebar).
- **Unidentified files**: `GET $A/library/unidentified?limit=100` (follow `next_cursor`), or the page `/unidentified` in
  the web interface. Each entry says what was tried; `GET $A/library/unidentified/{id}/attempts` has the detail.
- **Music:** albums with the *Unverified* badge (recognised as files, matched to nothing). Count them from the album list.
- **Wrong matches.** Open a sample of recognised works (at least 30, more if they look wrong often; spread across
  the library, not just the first page) and compare with the files: wrong album, wrong edition, wrong series, wrong
  season or episode, wrong film year, wrong book. The work page's *Files* tab shows which file went where.

Recognition rate = recognised files / files seen. Report unverified and unidentified separately from wrong matches: a
wrong match is worse than no match.

## Step 6 (optional): the \*Arr import

In the web interface: *Connections → \*Arr*, add the \*Arr (address, API key; **"Send monitoring back" off**), open its
preview, map it to a library, and run the import into the throwaway. Record: did the connection work, did the preview
match what the \*Arr holds (counts per type), what failed or looked odd, how long it took. Then revert the import from
its history entry and note whether the revert was clean.

## Step 7: write the report

Write `solarr-recognition-report-YYYY-MM-DD.md` in the person's current folder:

```markdown
## Recognition test: <media types>, <N> files

**Solarr version:** 2026.xx.x.xxxx (image ghcr.io/makearr/solarr:latest, pulled YYYY-MM-DD)
**Setup:** read-only mount, read_only mode, keys: TMDB yes/no, AcoustID yes/no
**Library:** <type>, <files>, <how it is organised: tidy by *Arr / mixed / chaos>, names: real | anonymised

### Results
| Library | Files seen | Recognised | Unverified | Unidentified | Wrong in sample | Scan + settle time |
|---|---|---|---|---|---|---|

### Wrong matches
| File (path below the library root) | Solarr matched | Should be (provider link if known) | Notes |
|---|---|---|---|

### Not recognised (most common patterns first)
- Pattern: `<name shape>` (N files). Example: ... What Solarr tried: ...

### *Arr import (if tested)
...

### Anything else
Confusing screens, errors in Settings → Logs (copy the message, not the whole log), slowness.
```

For a wrong or missing match, the most useful details are: the file name and its folder, embedded tags
(`ffprobe -hide_banner -show_format -show_streams <file>` for audio/video; strip anything personal), and the correct
item at a provider (a MusicBrainz release, TheTVDB/TMDB page, Open Library work).

Then show the report to the person, ask for changes, and print:

```sh
gh issue create --repo makearr/Solarr --title "Recognition test: <types>, <N> files" --body-file solarr-recognition-report-YYYY-MM-DD.md --label recognition
```

(If the label does not exist, drop `--label`. Without `gh`: paste the file at https://github.com/makearr/Solarr/issues/new.)

## Step 8: clean up

`docker rm -f solarr-recognition-test`, confirm with `docker ps -a | grep solarr-recognition-test` (no output), and ask
before deleting `$SCRATCH`.
