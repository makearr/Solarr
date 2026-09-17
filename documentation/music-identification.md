# Music identification

How a scanned or imported music file comes to belong to an album. Code:
`internal/media/music/consumer.go` (scan), `inspect_identify.go` (import),
`album_match.go`, `album_identify.go`, `album_local.go`, `edition_choice.go`.

## Passes

A scan batch is one directory's files. Each pass only sees what the previous ones left.

1. **Embedded MBIDs**, per file, no network (`MBIDIdentifier`). Unchanged.
2. **Album matching**: the remaining files, grouped by album tag, scored together against
   whole release tracklists. Identified → every file gets the release group *and* the release
   (edition). Ambiguous → queued with the rival albums. Anything else → next pass.
3. **Per-file text search** (`SearchIdentifier`): the older title-only match.
4. **Folder siblings**: an unresolved file joins the one album its identified siblings agree on.

Import runs the chain per unit, then `RefineUnitByAlbum`, which replaces anything but an
embedded-MBID answer with an album match. Enrichment's edition choice for a work identified
without an edition uses the same scorer (`chooseRelease`).

## Recall

Up to 10 release groups per folder, in evidence order: the release group the identified
siblings agree on; an artist + album search (the album tag cleaned of edition noise, and raw
when that differs — "(2008 mix)" is part of a real title); then, when the tags are missing or
recalled fewer than two groups, recording search on up to four track titles, keeping the five
release groups most tracks appear on, studio albums before compilations. Every release of each
group is scored. Releases and recording searches are cached like everything else.

## Scoring

The method is Lidarr's distance as DroppedNeedle adapted it. A distance is the weighted mean of
penalties in [0,1].

Per file against per track: title (weight 3, normalised Levenshtein), length (2; free within
10 s, full at 40 s), position (1; absolute or per-disc), recording MBID (10). Files and tracks
are paired by optimal assignment (`match.Assign`), unpaired at cost 2.

Per release: artist (3), album title (3), year (1, over ten years), each pair's distance (2),
unmatched files (0.9), release type (3: album 0, EP 0.3, other 0.5, compilation or live 1).
Missing tracks are never counted, so a partial album still matches its own release.

A value read from the path instead of a tag counts at half strength; a placeholder such as
"Unknown Artist" abstains.

## Decision

A release is **accepted** when its distance without release type is at most 0.20, at least 60%
of the files pair, the artist is plausibly the credited one, at least two files (one for a
single file) are supported by a close title or a matching recording, and no more than a
quarter of the files are **contradicted** — a same-named track whose length is more than 30 s
off, or a different recording MBID.

Accepted releases rank by distance, then official, track count closest to the folder, earliest
date. The best is **identified** unless a different release group's best is within 0.05, which
is **ambiguous** — except that an official release beats a rival known only as a promo or
bootleg. A contradicted folder falls through to text search rather than being queued: two
real singles were longer edits of the catalogued recording.

DroppedNeedle also refuses a win when the runner-up leaves some file without a supported
counterpart; its code does the opposite of its own docstring there, so that clause was not
taken.

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
