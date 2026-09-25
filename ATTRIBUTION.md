# Attribution

Solarr is licensed **AGPL-3.0-or-later** (see `LICENSE`).

This file records third-party code incorporated into Solarr and the obligations that come
with it. **Update it in the same commit as the code it describes** — reconstructing provenance
later is unreliable, and AGPL-3.0 §5 requires that modified files carry prominent notice.

---

## Incorporated code

### Release-name and quality parsing — from Lidarr (GPL-3.0)

- Source: `github.com/Lidarr/Lidarr`, `develop` branch, fetched 2026-09-04 via direct
  `raw.githubusercontent.com` access (verified working from this sandbox for the first time —
  earlier sessions had no outbound network at all and wrote an original grammar instead; see
  "Corpus data" below for that history).
  - `src/NzbDrone.Core/Parser/Parser.cs` (868 lines fetched)
  - `src/NzbDrone.Core/Parser/QualityParser.cs` (693 lines fetched)
- Taken, at the scope ADR-18's "The port, scoped" subsection decided:
  - Music identity: `ReportAlbumTitleRegex`'s 14 patterns (discography and artist/album/year
    shapes), `AirDateRegex`, `SixDigitAirDateRegex`. `ReportMusicTitleRegex` (per-track, not
    per-release) audited but not wired in — nothing in this codebase parses track filenames yet.
  - Quality vocabulary: `BitRateRegex`, `SampleSizeRegex`, `CodecRegex`, `WebRegex`,
    `ProperRegex`, `RepackRegex`, `VersionRegex`, `RealRegex`.
  - Media-type-independent (all 21 named in ADR-18): `NormalizeRegex`, `SimpleTitleRegex`,
    `WebsitePrefixRegex`, `WebsitePostfixRegex`, `ReleaseTokenPrefixRegex`,
    `CleanReleaseGroupRegex`, `CleanTorrentSuffixRegex`, `ReleaseGroupRegex`,
    `AnimeReleaseGroupRegex`, `ExceptionReleaseGroupRegex`, `ExceptionReleaseGroupRegexExact`,
    `RejectHashedReleasesRegex`, `RequestInfoRegex`, `CommonTagRegex`, `BracketRegex`,
    `AfterDashRegex`, `WordDelimiterRegex`, `PunctuationRegex`, `CommonWordRegex`,
    `DuplicateSpacesRegex`, `FileExtensionRegex`, `PercentRegex`.
- Changed:
  - **RE2 has no backreferences or lookaround.** `PercentRegex`, `NormalizeRegex`,
    `ReleaseGroupRegex`, `ExceptionReleaseGroupRegex`, `AnimeReleaseGroupRegex`, `VersionRegex`,
    `AirDateRegex` and `SixDigitAirDateRegex` all used one or the other. Each is rewritten in
    `internal/identify/parse/lidarr.go` as an explicit two-pass match — find without the assertion, then
    check the same condition directly against the surrounding bytes — documented at each
    function. No upstream pattern used an actual backreference (`\1`/`\k<name>`) in this scope;
    only lookaround needed rewriting. Proven against the golden corpus (`testdata/releases/`),
    not against the original .NET engine — this project has no .NET runtime to compare against.
  - `ReleaseGroupRegex`'s original also reuses one named group (`releasegroup`) across both
    alternatives, which Go's `regexp` rejects outright (duplicate group names), on top of its
    lookaround — a second, independent reason it needed the two-pass rewrite rather than a
    direct port.
  - **The flat `Quality` enum is not taken.** Lidarr collapses codec, bitrate and bit depth into
    values like `FLAC_24` and detects only 24-bit with no sample-rate concept; Solarr keeps
    the axes orthogonal (`model.Attributes.Format`/`BitrateKbps`/`BitDepth`), so the port takes
    the regex vocabulary and maps captures onto separate fields instead of an enum (ADR-40).
  - **Two known-good real-world regressions found live and fixed, deliberately diverging from
    a literal port**: (1) `ReleaseGroupRegex`'s bracket alternative has no codec-word exclusion
    the way its dash alternative does, so a trailing quality bracket like `[FLAC]` or `[WEB]`
    was captured as if it were the group on several golden-corpus titles that have no real scene
    group at all — excluded by value, not just by lookahead. (2) `ReleaseGroupRegex` runs on the
    *whole* release title upstream, which reads a hyphenated artist name ("Jay-Z", "Bovo
    Thugs-n-Sabedup") as its own dash-token candidate; the artist portion is stripped first here
    (`stripArtistPrefixForGroup`) before the cascade runs. Both are recorded as behaviour changes
    per the reuse table's own obligation, not silent fixes.
  - `internal/identify/parse/parse.go`'s own artist/album segmentation (dash-splitting, per-segment
    trailing-token trimming) is **not** replaced by Lidarr's literal `ReportAlbumTitleRegex`
    cascade — it is a smaller, generic engine this project already had, hardened against real
    scene names during 2026-09-03's live verification, and measurably more accurate on at least
    one directly-comparable ambiguous case: Lidarr's own regex, run on
    `"Gotabam Bih-2007-The Sebitikoda Hopin-FTD"` vs `"Gotabam Bih-The Sebitikoda Hopin-2007-FTD"`,
    disagrees with itself on whether `FTD` is a release group depending on where the year falls,
    with no principled way to tell which answer is right. The corpus fixture for the first form
    was corrected 2026-09-04 to agree with the second (`release_group: "FTD"` in both), since
    genuine upstream behaviour, once actually available to test against, turned out to be the
    more consistent — and more defensible — answer than the guess an earlier session made without
    it.
- Files: `internal/identify/parse/lidarr.go` (new), `internal/identify/parse/parse.go` (wiring),
  `internal/identify/model/attributes.go` (`BitDepth`, `ProperRepack` fields).
- **Deferred, not forgotten** (ADR-18's own text: "so 'everything is ported' stays a checkable
  claim rather than an assumption"):

  | Regex group | Source | Status |
  |---|---|---|
  | TV identity — season/episode/absolute/daily/multi-episode | Sonarr `Parser.cs` | Not fetched. Lands at stage 8, when a consumer exists |
  | Movie identity — edition and year-disambiguation | Radarr `Parser.cs` | Not fetched. Lands at stage 10, when a consumer exists |

When code is incorporated, record it as:

```
### <component> — from <project> (<licence>)

- Source: <repository>, <commit or version>
- Taken:  <what exactly — files, functions, data tables>
- Changed: <how it was modified>
- Files:  <where it lives in this repository>
```

---

## Corpus data (not code)

`internal/identify/parse/testdata/releases/seed.jsonl` — the ADR-18 parser golden corpus's original 45
seed entries use real release-name strings drawn from Lidarr's own public test fixtures
(`src/NzbDrone.Core.Test/ParserTests/ParserFixture.cs`, `Lidarr/Lidarr`, GPL-3.0). ADR-18 is
explicit that this needs no permission at all ("release names are facts and short strings;
collecting them raises no licensing concern") and it is not incorporated code — recorded here
anyway for transparency.

`internal/identify/parse/testdata/releases/download_folder.jsonl` (274 entries) and
`user_verified.jsonl` (15 entries) — grown 2026-09-04 from directory names in the user's own
real `/mnt/downloads/sabnzbd` folder (never the audio itself; ADR-18's "release names are facts
and short strings" applies the same way here). Adult-content and video-only names were excluded
as out of scope for a music corpus. Six real names whose correct parse is still ambiguous or
unhandled (an audiophile vinyl mastering code with no surrounding delimiter, a dual-format
`wav+mp3` suffix, and four comma-and-plus-joined boxset track listings) were deliberately left
out rather than encoded as if their current, imperfect output were ground truth.

**Historical note, kept for the record**: earlier sessions (through 2026-09-03) had no outbound
network access at all, so Lidarr's actual C# source could only have reached this project through
a summarising fetch — unsafe to trust for regex syntax, where one wrong escape changes behaviour
silently. `internal/identify/parse/parse.go`'s artist/album segmentation was written as an original
grammar instead, proven against the corpus above. **This sandbox gained real, verified network
access 2026-09-04** (a direct `curl` to `raw.githubusercontent.com` returns exact bytes), which
is what unblocked the real port recorded under "Incorporated code" above. The original grammar
was kept rather than replaced wholesale — see that entry for why.

## Sanctioned reuse

Radarr, Sonarr, Lidarr, Readarr, Whisparr and Prowlarr are **GPL-3.0**, which is compatible
with AGPL-3.0. Reuse is deliberately limited to two areas:

| Area | Rationale |
|---|---|
| **Release-name parsing** — regexes, token tables, season/episode format handling | A decade of accreted edge cases that is hard to rebuild and boring to get wrong |
| **Quality definitions** — resolution, source, codec and edition token vocabularies | Shared vocabulary with the wider ecosystem; divergence would be a bug, not a feature |

**Anything beyond these two areas requires an explicit decision** with the trade-offs stated
before it is taken. The reason is not legal but architectural: the more that is imported, the
more the design drifts back toward the one Solarr exists to move away from. When something
outside this scope looks worth taking, raise it — do not simply take it.

## Dependencies

Linked libraries, not incorporated code — they are not modified and AGPL-3.0 §5 does not
apply to them. They are listed because the About view surfaces this file to satisfy §13, and
a user offered the Corresponding Source should be able to see what the binary links.

| Module | Licence | Why |
|---|---|---|
| `modernc.org/sqlite` | BSD-3-Clause | Pure-Go SQLite. Keeps `CGO_ENABLED=0` building, which ADR-30 requires so the image can bundle its media tools rather than link them. Provisional pending the ADR-5 benchmark |
| `github.com/goccy/go-yaml` | MIT | `config.yml` parsing. Go has no stdlib YAML, and ADR-44 needs a parser that reports positions. Chosen over the archived `gopkg.in/yaml.v3` |
| `github.com/robfig/cron/v3` | MIT | Cron parsing for the scheduler's task cadences. The same parser validates on write and schedules later, so an accepted cadence cannot be rejected by the thing that runs it |
| `github.com/go-chi/chi/v5` | MIT | HTTP routing. Everything stays an `http.Handler`, so the router owns no part of the code's shape (ADR-31) |
| `golang.org/x/crypto` | BSD-3-Clause | argon2id password hashing (ADR-14). Not in the standard library, and hand-rolling a password hash is never the right call |
| `golang.org/x/image` | BSD-3-Clause | High-quality resampling (`draw.CatmullRom`) for section 21.6's artwork pipeline |
| `github.com/mayahiro/go-webp` | MIT | Pure-Go lossy (VP8) WebP encoder for section 21.6. ADR-30 rules out cgo's libwebp; chosen as the first of three candidates tried, verified by a real encode-then-decode round trip rather than trusting its README |
| `github.com/nwaples/rardecode/v2` | BSD-2-Clause | Pure-Go RAR decoder (ADR-49), including multi-volume sets. `zip`/`tar`/`gzip`/`bzip2` are stdlib and need no entry here |
| `github.com/bodgit/sevenzip` | BSD-3-Clause | Pure-Go 7z decoder (ADR-49) |
| `github.com/ulikunitz/xz` | BSD-3-Clause | Pure-Go xz decompression (ADR-49), for `.tar.xz` |

Bundling `unrar` or p7zip was considered and rejected (ADR-49): the UnRAR licence forbids using
the source to recreate the RAR compression algorithm, which is exactly the kind of licence
friction this file exists to avoid inside an AGPL-3.0 image; p7zip's own RAR support inherits
the same code. `bodgit/sevenzip`'s own transitive dependencies —
`github.com/bodgit/plumbing`, `github.com/bodgit/windows`, `github.com/spf13/afero`,
`github.com/klauspost/compress`, `github.com/pierrec/lz4/v4`, `github.com/stangelandcl/ppmd`,
`github.com/nwaples/rardecode` (the non-v2 line, a 7z-embedded-RAR helper), `go4.org` and
`github.com/andybalholm/brotli` — are BSD or MIT. `go.mod` is the authoritative list.

`modernc.org/sqlite`'s transitive dependencies — `modernc.org/libc`, `modernc.org/memory`, `modernc.org/mathutil`,
`github.com/remyoudompheng/bigfft`, `github.com/dustin/go-humanize`, `github.com/google/uuid`,
`github.com/mattn/go-isatty`, `github.com/ncruces/go-strftime` and `golang.org/x/sys` — are all
BSD-3-Clause or MIT. `go.mod` is the authoritative list.

---

## Third-party services

**Servarr metadata proxies** — `skyhook.sonarr.tv`, `api.radarr.video`, `api.lidarr.audio` —
are supported as metadata providers. These are not covered by any source licence; they are
hosted services operated by the Servarr project for their own applications, with no published
terms permitting third-party use.

Conditions under which Solarr uses them:

- **Honest identification.** The User-Agent identifies Solarr and its version and links to
  this repository. It never impersonates another application, and no attempt is made to evade
  rate limiting or blocking.
- **Conservative request rates**, more conservative than for credentials Solarr owns.
- **Never a hard dependency.** They are one provider among several (ADR-8). Direct TMDB, TVDB
  and MusicBrainz providers are configurable as fallback, and the circuit breaker fails over
  automatically. If access is withdrawn or blocked, the library keeps working.
- **Aggressive caching** so a given lookup is made once (ADR-29).

They may change or withdraw access at any time without notice, and that is their prerogative.

**TheAudioDB** (`theaudiodb.com`) supplies artist photos and biographies, and album covers, where
Wikidata and the Cover Art Archive have none. Its free public API key is used unless the person
enters their own supporter key. The images and texts are contributed by TheAudioDB's users; they
are fetched for the person's own library and cached locally, never redistributed by this project.
It can be switched off under Settings -> Metadata, and removed from the cover sources.

## Not sanctioned

- Trademarks and branding. The names and logos are not covered by the source licence.
- Impersonating another application's User-Agent, or any evasion of rate limits or blocks.

---

## Obligations this licence creates

1. **Source availability on network use** (AGPL §13). Anyone running a modified Solarr as
   a network service must offer users its complete source. The web UI carries a visible link
   to the source for the running version — see `documentation/licence-compliance.md`.
2. **Modified files carry notice** of the change and its date.
3. **Copyright notices are preserved** in anything derived from another project.
4. **Derivative works stay AGPL-3.0-or-later.**
