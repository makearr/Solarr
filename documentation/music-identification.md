# Music identification

How a scanned or imported music file comes to belong to an album. Code:
`internal/media/music/consumer.go` (scan), `inspect_identify.go` (import),
`album_match.go`, `album_identify.go`, `album_local.go`, `edition_choice.go`.

## Passes

A scan batch is one directory's files. Each pass only sees what the previous ones left.

1. **Embedded MBIDs**, per file (`MBIDIdentifier`). Each distinct release group is looked up once
   per folder (cached; enrichment needs it anyway): a merged id is stored as the group it was
   merged into; a retired one, 404 at every source, is recorded `retired` and the files go on to
   album matching like untagged ones. An unreachable source trusts the tags.
2. **Album matching** (a release the files name -- their album tag as written, else their
   folder -- beats one they do not, whatever its release type): the remaining files, grouped by album tag, scored together against
   whole release tracklists. A group that fits nothing as a whole while its track numbers
   collide is split by the track total each file claims ("3/10" against "3") and each part
   matched alone: an album's first half and its single's remixes under one album tag.
   Identified → every file the release has a track for gets the release group *and* the
   release (edition); a second copy of a track is placed on the track it copies, uncounted; a
   file it has no track for is recorded `unplaced`, matched once more
   against the other albums the folder's identified files name, and otherwise left to the later
   passes, where the folder's siblings usually join it — unless they disagree, as beside a
   single's own tagged files. Ambiguous → queued with the rival albums. Anything else → next pass.
3. **Per-file text search** (`SearchIdentifier`): the older title-only match. It never answers
   an album the album pass found no track for this file on: that is the tag claim the tracklist
   already contradicted.
4. **Fingerprints**: what is left is fingerprinted, named by AcoustID and matched as an album
   with those names (see below); a named file whose song is on the album the folder's identified
   files agree on is left to the passes after, never matched alone to another album. Skipped,
   and recorded as skipped, with no API key configured.
5. **Track by track** (`track_match`, 2026-09-27): a file whose folder settled as no album --
   a playlist, a track Lidarr sorted into the wrong album folder -- is identified on its own.
   Its recording is the one its fingerprint or embedded recording id names, else a recording
   search by artist and title within 5 s of its length. **The folder is a hint, never a
   certainty**: of every release group that recording is on -- the folder's own included, when
   another of its editions carries the track the matched edition lacks -- the album the folder,
   the album tag or the identified siblings name wins; otherwise a studio album before an EP, a
   single, then a compilation, live, remix or DJ-mix release, the earliest first. A file stays
   with the album its siblings agree on when that album has its song, or when only a title
   search points elsewhere (one standard, many takes); a fingerprint or an embedded recording id
   of a song the album lacks moves it. An ambiguous album stays queued.
6. **Folder siblings**: an unresolved file joins the one album its identified siblings agree on,
   at a confidence of 0.5 -- with nothing contradicting it, the folder is the best hint left.

A file identified to another album than its folder's is not moved by the scan: the library
rename (Settings -> the library's naming, "apply to library") previews and moves it.

Import runs the chain per unit, then `RefineUnitByAlbum`, which replaces anything but an
embedded-MBID answer with an album match. Enrichment's edition choice for a work identified
without an edition uses the same scorer (`chooseRelease`).

## Recall

Up to 10 release groups per folder, in evidence order: the release group the identified
siblings agree on; an artist + album search (the album tag cleaned of edition noise, and raw
when that differs — "(2008 mix)" is part of a real title); then, when the tags are missing or
recalled fewer than two groups, recording search on up to four track titles, keeping the five
release groups most tracks appear on, studio albums before compilations; last, the album the
folder names, when it differs from the tag — a malformed tag can hide the title — never counted
as the tags' recall, since a folder name misreads easily ("Mazotuz - The Remixes!"). Every release of each
group is scored. Releases and recording searches are cached like everything else.

The search index still lists merged release groups under their old ids, so one album can be
recalled twice and tie with itself. A release belongs to exactly one group, so two groups
sharing a release are one: it is scored once, under the id a lookup of the later one answers.
The artist is compared against both the credited name and the artists' own names, from search
and lookup alike ("Tupac" credits 2Ibe, "Gohudo" credits Gohudo Cosicatebi).

## Scoring

The method is Lidarr's distance as DroppedNeedle adapted it. A distance is the weighted mean of
penalties in [0,1].

Per file against per track: title (weight 3, normalised Levenshtein), length (2; free within
10 s, full at 40 s), position (1; absolute or per-disc), recording MBID (10). When at least
half the files share their disc and track with a different song, every disc claim in the folder
abstains: a three-disc set tagged disc 3 throughout is otherwise scored as if two of its discs
were misplaced. Copies of one song sharing a number prove only duplicate files. Files and tracks
are paired by optimal assignment (`match.Assign`), unpaired at cost 2.

Per release: artist (3), album title (3), year (1, over ten years), each pair's distance (2),
unmatched files (0.9), release type (3: album 0, EP 0.3, other 0.5, compilation or live 1).
The album title is the best of the tag, the cleaned tag and the folder's album name.
Missing tracks are never counted, so a partial album still matches its own release.

A value read from the path instead of a tag counts at half strength. The artist is read from the
tags, then the file name ("Artist - 03 - Title"), then the folders. A value that says nothing —
a placeholder such as "Unknown Artist", or a Windows short name such as "DORA9E~V" — gives way
to the next source, and abstains only when no source says more.

## Decision

A release is **accepted** when its distance without release type is at most 0.20, at least 60%
of the files pair, the artist is plausibly the credited one, at least two files (one for a
single file) are supported by a close title or a matching recording, and no more than a
quarter of the files are **contradicted** — a same-named track whose length is more than 30 s
off, or a different recording MBID.

Accepted releases rank by distance, then official, track count closest to the folder, earliest
date. The best is **identified** unless a different release group's best is within 0.05, which
is **ambiguous** — except that an official release beats a rival known only as a promo or
bootleg, and a best release the folder fills exactly (every file paired, as many files as
tracks) beats rivals it only partly covers: 12 files are the 12-track album, not 12 of a
26-track compilation of the same name. Completeness never lifts a worse fit over a better one.
A perfect fit (distance 0: every claim agrees) beats any rival some claim contradicts, and
the album the folder's identified files agree on beats any close rival: their embedded ids
outweigh a tracklist near-tie for one untagged neighbour.
Nor is it ambiguous when every close rival holds the best's tracklist — as many tracks, every
file on the same recording: that is one album catalogued twice, decided by the ranking above. A contradicted folder falls through to text search rather than being queued: two
real singles were longer edits of the catalogued recording.

DroppedNeedle also refuses a win when the runner-up leaves some file without a supported
counterpart; its code does the opposite of its own docstring there, so that clause was not
taken.

## Confidence and precedence

Every identification carries two things (2026-09-24). **Precedence** is what kind of evidence it
is — asserted (an embedded MBID, a person's choice) > matched (a folder against a tracklist) >
searched (text or AcoustID lookup) > inferred (a folder sibling) — and alone decides that a
guess never outranks an assertion. **Confidence** is how likely it is to be right, calibrated on
the measurements below, and alone is compared with the one threshold,
`import.auto_commit_confidence` (0.90), for scan and import alike:

| Evidence | Confidence |
|---|---|
| Release group MBID / release MBID / recording MBID tag | 0.98 / 0.97 / 0.80 |
| Album match | 0.99 − 0.5 × distance (0.89 at the 0.20 accept limit) |
| Clean text search | 0.95 − 0.5 × distance |
| AcoustID | its own score |

An identified album below the threshold is held for review like an ambiguous one. Before this,
album matches were squeezed under 0.68 to rank below tags and then bypassed the threshold
entirely, so the setting did nothing for most imports. Scanning the test library gives the same
identity for all ~1,100 files before and after.

## The record, and the manual decision

Every run for a file its embedded MBIDs did not settle is stored (`identification_attempt`,
the newest five per file): the outcome — `identified`, `queued`, or `retry` after a lookup
failure — and each pass in order with its outcome and the candidates it weighed. A candidate's
evidence is structured: `title`, `artist`, `release_type`, `edition` and its `edition_title`,
`edition_date`, `edition_country`, `edition_status`, `edition_tracks`, `files_matched`,
`files_supported`, `distance`, `accepted`, `contradicted`, `recall`, `outcome`.

A queued file's **Review** page (`/unidentified/<media object>`,
`GET /library/unidentified/{id}/attempts`) shows the latest run pass by pass, every candidate
with its evidence, and lets the user pick any album, not only the top guess. An import unit
waiting for a decision shows its candidates the same way.

## What import keeps

A unit album matching identified is committed with the release it matched as the edition, and
each file is named from the track it matched rather than its own tags. Multi-disc releases
therefore name files `Artist - 1-06 - Title` (`music.TrackPosition`); single-disc releases keep
`Artist - 06 - Title`, so an existing single-disc library still proposes no renames. Without
this, enrichment chose the edition again from tags alone and picked a 15-track release for 17
matched files, and a 3-CD set put three files numbered `06` in one folder (found 2026-09-17).

## Fingerprints (AcoustID)

The last pass, for files no earlier one could name. Every still-unidentified file in the batch
is fingerprinted in one `fpcalc` call, each fingerprint is looked up, and what comes back is a
*name* — artist and title — not an album. Those names then go through the same album matching
pass 2 uses: whole tracklists, scored together, seeded with the release groups AcoustID named,
most agreed-on first. AcoustID identifies; the ordered metadata sources supply the data.

Asking a single file which album it belongs to is unanswerable: one recording appears on its
album, its single, and every compilation since. A folder answers it, which is why this pass
matches albums rather than files. With no album lookup configured it falls back to per-file
matching, which can answer only when AcoustID named exactly one release group.

Two rules earned by measuring against the real service (2026-09-18):

- **The folder outvotes a wrong link.** AcoustID's database is crowd-contributed, and a real
  Heka track came back with a recording by a band called Badtown Boys listed *first*. The
  artist most of the folder's files name decides which candidate each file is; one file cannot
  tell which of its own candidates is the wrong one.
- **The recording id is never asserted.** AcoustID names every recording MusicBrainz holds of
  one performance. Passing one of them to the matcher refutes every release that uses another
  — which is what happened: the correct album was rejected for all 12 of its tracks. Only the
  names and the file's own decoded length are used.

A match below 0.70 is dropped, and a match naming nothing identifies nothing. A work the
matcher identifies is named after its release group, not after the folder it sits in: an
untagged rip lives in "Ripped/CD01", and that is a fact about our directories, not the record.

It needs an **application** API key, free from https://acoustid.org/new-application, entered
under Settings → Metadata. The key an AcoustID account shows on its own page is a different
one, for submitting fingerprints, and a lookup refuses it. Stored sealed in `settings.db`
(`metadata.acoustid.api_key`), never returned by the API — `GET /metadata/settings` says only
whether one exists, `POST /metadata/acoustid/apikey` replaces it and an empty key clears it.
With no key the pass reports itself disabled and does not fingerprint at all, so identification
keeps working with one fewer pass. Switching `metadata.acoustid` off does the same with a key
stored; switching it back on re-runs every file it skipped.

Every file's outcome is recorded on its own identification attempt, including
`fingerprint: disabled_no_key`: a file skipped for want of a key is not a file AcoustID had no
answer for, and only the record tells them apart once a key is entered.

The `meta` parameter is a **space**-separated list. Sent with a literal `+`, AcoustID answers
`ok` carrying bare AcoustID ids and no metadata rather than refusing, and every result is then
discarded as naming nothing.

## Lookup priority

MusicBrainz, BrainzMash and the Lidarr proxy each have one rate-limit bucket. Inside a bucket
a due request goes to the most urgent waiter (`httpx.WithPriority`, set by
`musicbrainz.Provider` from the caller's `Priority`): interactive, then identification, then
enrichment, backfill and refresh. A user's lookup therefore never queues behind a scan.

## Measured, 2026-09-17

`/storage/media/music`, ~170 folders, ~1,100 files. In the 122 folders whose files agree on an
embedded release group, every MBID was hidden and the result compared against it:

| | Correct | Wrong | Unresolved |
|---|---|---|---|
| Text search per file, folder agreement | 105 | 0 | 17 |
| Album matching alone | 120 | 0 | 2 |
| Album matching, then text search | **122** | **0** | **0** |

The chosen edition equalled Lidarr's in 102 of 120; the rest were pressings with the same
track count and distance, differing only in country or date. Of the 47 folders without an
embedded release group, 46 were identified — all plausible on inspection — and one, a
74-track box set fitting two catalogued box sets within 0.007, was ambiguous.
