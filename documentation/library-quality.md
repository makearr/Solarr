# Library quality

> **Level: 🟡 Advanced.** Quality profiles, cutoff and target for a library.

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
- **Target** is one named quality a library aims for. Targets are made under Quality profiles (Target qualities), several
  per media type, and a library picks one on its own Quality page; editing a target changes every library aiming for it, and
  one a library still aims for cannot be removed. A target names the container (one file type, chosen from a list), the
  picture (resolution, codec, HDR, frame rate, video bitrate and **CRF**), the sound (codec, channels, bitrate, languages)
  and subtitle languages; a part left on "any" is free, and a part the probe could not read is not held against a file.
  **CRF** is read from the encoder settings an x264 or x265 encode writes into the file's first frame (the probe reads the first
  512 KiB of a video file once); a file at or below the target's CRF meets it, a file that states none is not held against it.
  Audio targets keep codec, file type and a bitrate within a tolerance, plus channels (mono or stereo), **sample rate** and
  **bit depth** (music), and **chapter marks** (audiobooks, stored, not yet judged). A video target also stores an **encoder
  preset** (x265 "slower"), a **frame-rate cap** for "source frame rate (max 60)" and a colour depth (stored, not judged).
  Every target has a **label** ("Space conscious") and a description. Migration 0063 ships presets named after their specs
  (11 video ones each for movies, series and adult, 9 music, 8 audiobook, 1 book), one labelled "Space conscious" per media
  type; they are ordinary rows, editable and removable. The edit form's **Name from specs** button renames a target after
  what it asks for.
  A target is also the setting a later transcode will aim for, so every file and every candidate release is judged by **how
  close** it is, not only whether it meets it (`quality.Distance`): 0 to 100 %, a weighted share of the parts both name
  (resolution, codec, CRF and video bitrate weigh most; a number loses a quarter per half-octave away; a part the probe or
  release name does not state is left out). A library file shows "82% of the target" with the gaps on hover; a candidate's
  decision carries a zero-point `target-closeness` component. The closeness ranks nothing; the tiers below do.
  **Content profiles** (migration 0064; Quality profiles > Content profiles) are what kind of material a target is for:
  video 4K, Sports, Anime, Old movies, TV series, Cartoon, CRT / VHS; audio Radio, CD, Lossless, Spoken, Spoken (mono).
  A profile carries suggested settings (resolution, CRF, encoder tune such as animation or grain, deinterlace, channels, rates)
  and the shipped target that fits it and is nearest the preferred "Space conscious" settings. A target picks one and
  "Apply its recommended settings" fills the form; every number stays editable. Encoder tune is stored for the transcode;
  a deinterlace target counts an interlaced file as short of it. 480p, 576p and CD-quality presets were added for them.
  Two checkboxes shape how a target ranks what is found (`judge.WithTarget`): **Prefer over** (transcoding brings quality
  down, never up) ranks the resolutions above the target (the nearest first) before the ones below it, whatever the profile
  lists, and ranks a bitrate, frame rate or similar number above its ideal before any below it, however near the lower one is
  (a 1080p target takes 4k over 720p; a 1,600 kbit/s target takes 6,000 over 1,500); closeness counts above as met;
  **Ignore everything below the target while anything at or above exists** leaves the
  resolutions below the target unranked, so they win only when nothing at or above is found.
- **Size limits per quality** (Radarr's quality definitions): a video profile may carry, per resolution, a minimum,
  preferred and maximum in MB a minute (`targets.overall_bitrate` with `when.resolution`, gate `require.size =
  "reject_outside_range"`). A release below the minimum (starved) or above the maximum (bloated) is refused; one whose length
  is not known is not. "Rank nearer the preferred size first" adds `overall_bitrate_distance` to the comparison (off by
  default: it ranks within equal tiers, so it can make a replacement count as an upgrade). Migration 0065 gives the shipped
  video profiles (not the Any ones) suggested limits, so a release smaller than a profile's minimum is now refused; the
  profile form's "Use the suggested limits" fills the same numbers. Music and audiobook ranges are the bitrate ranges the audio form already has.
- **Quality profiles are an expert setting and target qualities and content profiles advanced ones**, both administrator
  only: a library's own Quality page shows the profile order at expert and the target choice at advanced.
- **The target must sit inside the library's profiles.** The Quality screen warns when no profile takes the target, and when the
  first profile (the one it leads with) does not though a later one does; either way it offers the shipped targets of the media
  type that the first profile takes, the nearest the preferred "Space conscious" settings first, as one-click choices. A video
  target's overall bitrate (video plus audio) is checked against the size limits.
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
