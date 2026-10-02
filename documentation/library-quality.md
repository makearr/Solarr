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
(UPGRADABLE).

Searching a cutoff-unmet album judges every release against the album's **worst owned file**, by the
same profiles: a release must strictly beat it -- better by the quality tiers, or equal there and higher
in score -- or it is rejected as "not better than the files you have" (Invariant 72: ties keep the
incumbent). Cutoff Unmet offers Interactive search. A complete album wants nothing: searching it rejects
every release as "the files you have meet it", Lidarr's *existing files meet cutoff*.

**Upgrades allowed** (Lidarr's switch, per library, on by default): off, an album with every track
is complete whatever its quality -- the cutoff check marks nothing upgradable, so Cutoff Unmet is
empty and no upgrade is searched. Switching it runs the cutoff check at once.

**Replacing in place.** When a download's files are imported, Solarr measures them at once (the probe)
and then checks that album: for each track that now has an owned file and a downloaded one, the
strictly better file by the library's profiles stays, a tie keeping the owned one. The other goes to
the recycle bin -- reason `replaced` for an owned file an upgrade beat, `rejected_worse` for a download
that measured no better (facts win over the release name). Every path of a recycled file goes with it
(Invariant 92), and only tracks a Solarr download touched are ever settled this way: duplicates you
placed yourself are left alone. The nightly cutoff check does the same for anything missed.

**Preferred languages** (profile editor, 2026-10-02). Audio, lyrics and subtitle language, as two-letter codes,
are tie-breakers: each is a scoring rule named `Preferred <kind> language: <code>` worth +2, never a gate, so a
file that states no language is never refused. The probe reads them from the file's tags and stores them
(`media_attributes.languages`, `.lyrics_languages`): ID3 lyrics name their language in the key (`lyrics-deu`), a
Vorbis comment pairs `LYRICS` with `LANGUAGE`, an MP4 stream carries `language`; three-letter codes become
two-letter and `und` states nothing. Preferences apply to a *measured* file, so they decide between two files of
a track (replace in place), not between releases, whose names state no language. A file probed before this
existed has no lyrics language until it is probed again.

**Edit on an album.** Its own quality profile replaces the library's profile list for that album's search
and upgrade decisions (`desired_profile_override`), and the cutoff check judges its files against that profile alone (a file meets it
when it fits the profile). The per-file target/cutoff badges on the album page still show the library's.

**Delay profile** (Library -> Quality, D16): a library may prefer usenet or torrents and give it a head start. A release
of the other protocol is judged as usual, but while it is younger than the delay (counted from when the indexer posted it,
or from now when it states none) an acceptable one is held as a DEFER (`protocol-delay`, with the time it stops waiting),
then taken like any other. Nothing is ever filtered out, so an album only the other protocol has is still found. Default:
no preference, no delay; at most a week.
