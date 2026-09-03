# Attribution

MediaNexus is licensed **AGPL-3.0-or-later** (see `LICENSE`).

This file records third-party code incorporated into MediaNexus and the obligations that come
with it. **Update it in the same commit as the code it describes** — reconstructing provenance
later is unreliable, and AGPL-3.0 §5 requires that modified files carry prominent notice.

---

## Incorporated code

*None yet.*

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

`internal/parse/testdata/releases/seed.jsonl` — the ADR-18 parser golden corpus's seed
entries use real release-name strings drawn from Lidarr's own public test fixtures
(`src/NzbDrone.Core.Test/ParserTests/ParserFixture.cs`, `Lidarr/Lidarr`, GPL-3.0). ADR-18
is explicit that this needs no permission at all ("release names are facts and short
strings; collecting them raises no licensing concern") and it is not incorporated code —
recorded here anyway for transparency. **The parser logic itself (`internal/parse/parse.go`)
is an original implementation, not a port of Lidarr's C# regex parser**: this sandbox could
not reliably fetch and verify Lidarr's actual source byte-for-byte, so rather than risk an
inaccurate claim of reuse, the grammar was written from scratch and proven against the real
input strings above instead. If a future session gets reliable access to port the real
upstream regex/token tables, that would fall under "Release-name parsing" below and belongs
in "Incorporated code" once it happens.

## Sanctioned reuse

Radarr, Sonarr, Lidarr, Readarr, Whisparr and Prowlarr are **GPL-3.0**, which is compatible
with AGPL-3.0. Reuse is deliberately limited to two areas:

| Area | Rationale |
|---|---|
| **Release-name parsing** — regexes, token tables, season/episode format handling | A decade of accreted edge cases that is hard to rebuild and boring to get wrong |
| **Quality definitions** — resolution, source, codec and edition token vocabularies | Shared vocabulary with the wider ecosystem; divergence would be a bug, not a feature |

**Anything beyond these two areas requires an explicit decision** with the trade-offs stated
before it is taken. The reason is not legal but architectural: the more that is imported, the
more the design drifts back toward the one MediaNexus exists to move away from. When something
outside this scope looks worth taking, raise it — do not simply take it.

## Dependencies

Linked libraries, not incorporated code — they are not modified and AGPL-3.0 §5 does not
apply to them. They are listed because the About view surfaces this file to satisfy §13, and
a user offered the Corresponding Source should be able to see what the binary links.

| Module | Licence | Why |
|---|---|---|
| `modernc.org/sqlite` | BSD-3-Clause | Pure-Go SQLite. Keeps `CGO_ENABLED=0` building, which ADR-30 requires so the image can bundle its media tools rather than link them. Provisional pending the ADR-5 benchmark |
| `github.com/goccy/go-yaml` | MIT | `config.yml` parsing. Go has no stdlib YAML, and ADR-44 needs a parser that reports positions. Chosen over the archived `gopkg.in/yaml.v3` |
| `github.com/robfig/cron/v3` | MIT | Cron parsing for `backup.schedule`. The same parser validates at startup and schedules later, so a schedule accepted at boot cannot be rejected by the thing that runs it |
| `github.com/go-chi/chi/v5` | MIT | HTTP routing. Everything stays an `http.Handler`, so the router owns no part of the code's shape (ADR-31) |
| `golang.org/x/crypto` | BSD-3-Clause | argon2id password hashing (ADR-14). Not in the standard library, and hand-rolling a password hash is never the right call |
| `golang.org/x/image` | BSD-3-Clause | High-quality resampling (`draw.CatmullRom`) for section 21.6's artwork pipeline |
| `github.com/mayahiro/go-webp` | MIT | Pure-Go lossy (VP8) WebP encoder for section 21.6. ADR-30 rules out cgo's libwebp; chosen as the first of three candidates tried, verified by a real encode-then-decode round trip rather than trusting its README |

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

Conditions under which MediaNexus uses them:

- **Honest identification.** The User-Agent identifies MediaNexus and its version and links to
  this repository. It never impersonates another application, and no attempt is made to evade
  rate limiting or blocking.
- **Conservative request rates**, more conservative than for credentials MediaNexus owns.
- **Never a hard dependency.** They are one provider among several (ADR-8). Direct TMDB, TVDB
  and MusicBrainz providers are configurable as fallback, and the circuit breaker fails over
  automatically. If access is withdrawn or blocked, the library keeps working.
- **Aggressive caching** so a given lookup is made once (ADR-29).

They may change or withdraw access at any time without notice, and that is their prerogative.

## Not sanctioned

- Trademarks and branding. The names and logos are not covered by the source licence.
- Impersonating another application's User-Agent, or any evasion of rate limits or blocks.

---

## Obligations this licence creates

1. **Source availability on network use** (AGPL §13). Anyone running a modified MediaNexus as
   a network service must offer users its complete source. The web UI carries a visible link
   to the source for the running version — see `documentation/licence-compliance.md`.
2. **Modified files carry notice** of the change and its date.
3. **Copyright notices are preserved** in anything derived from another project.
4. **Derivative works stay AGPL-3.0-or-later.**
