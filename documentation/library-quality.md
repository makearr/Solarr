# Library quality

How a library says what quality it wants (the 2026-09-19 review, answered 2026-09-24). The
profiles themselves are ADR-40's rules: gates, then lexicographic tiers, then an additive score.

## The list

A library holds an **ordered list of profiles** (`category_quality_profile`). Earlier is
preferred; later ones are fallbacks; a profile not listed is never used. `category.quality_profile_id`
is kept equal to the first, so everything that reads one profile keeps working. An empty list
means the instance default: the shipped **Lossless preferred**, which accepts anything and ranks
lossless first, as Lidarr's default does (the no-op "Unconfigured" profile only if it was deleted).
Profiles carry a `suggested_rank`, the order a library is offered
("Space conscious, Lossy 320, Lossless preferred, then the rest" — the user's own example).

## Fitting a profile

`score.Fits`: every gate passes, every dimension the profile tiers is in its **first** tier, and
every measured value with a target is within that target's min and max. Plain acceptance is not
enough: the shipped profiles have no gates, so every release "passes" every one of them.

- **Search** judges a release by the first profile it fits; failing that, by the first whose
  gates accept it, ranked after every fit. The position is the leading tier of the quality vector,
  and a zero-point `profile-priority` component records it in the stored breakdown.
- **Cutoff** is one profile of the list. A file meets it when it meets the target, or fits a
  profile at or before the cutoff. Upgrades stop there.
- **Target** is one exact quality: codec (ffprobe's name), file type, and a bitrate range of
  ±`tolerance_pct` — no bitrate for a lossless codec. A file's type is its **extension**, not
  ffprobe's container, which for m4a is the demuxer list `mov,mp4,m4a,3gp,3g2,mj2`.
- **The warning** on the Quality screen fires when the target does not fit the first profile.

Files the probe has not measured yet are not judged either way.

## Cutoff unmet (M1d)

Lidarr's Wanted -> Cutoff Unmet. The **Check quality cutoff** task (daily at 02:40, after the probe;
Settings -> Scheduler, advanced) judges every monitored album that has all its tracks: when any of its
files, as the probe measured it, misses the library's target and fits no profile at or above the
cutoff, the album is **UPGRADABLE**; otherwise it is COMPLETE. An album with a file the probe has not
measured yet is left as it is. The judgment is the same one the album page shows per file
(`internal/identify/quality`).

**Library -> Wanted** has two lists: **Missing** (MISSING and PARTIAL) and **Cutoff Unmet**
(UPGRADABLE). Searching for upgrades is the next step and is not built yet, so Cutoff Unmet only lists.
