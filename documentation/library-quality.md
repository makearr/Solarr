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
  512 KiB of a video file once); a file at or below the target's CRF, or up to 2 above it, meets it; a file that states none is not held against it.
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
  down, never up): an item with **no file yet always** prefers over (`judge.WithFreshTarget`): the resolutions above the target
  rank, nearest first, before the ones below it (4k is nearer 1080p than 720p is), and a bitrate or sample rate above the ideal
  before one clearly below it. A number **just under** the ideal still counts as at it (25 % for video bitrates, 15 % for audio,
  10 % for the sample rate), so 1,500 kbit/s is nearer 1,600 than 6,000 is, while 800 is not. The box makes the same
  preference apply when **upgrading** a file you already have. Closeness counts at or above as met;
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

## Hesitant upgrades

A library is full of files already transcoded to the target, in Solarr or by Tdarr or FileFlows, and a constant-quality
encode lands near the target bitrate, not on it (a real library: 1080p at 24 fps from 130 to 7,900 kbit/s, all H.265 AAC MP4).
So `quality.MeetsTarget` takes the structure (container, codecs, resolution, frame rate, channels, languages) as the
judgement: a bitrate meets the target from 40 % of it (video, 70 % audio) up, a sample rate from 90 %, a bit depth from the target,
and a CRF at or below the target or up to 2 above it (a real library encoded at CRF 26 to 30). Such a file is **on target**: the item is complete, and `judge.UpgradeBeats` ignores the bitrate
distances against it, so no download replaces it for its size. Against a file that is off target a number must be nearer by half
an octave to count, and equal tiers keep the incumbent. The shipped presets' "30 fps" is a cap (source fps, max 30).
Ranges per quality stay opt-in; the shipped profiles carry none (migration 0066 took 0065's back).

## A target without a profile, and the automatic target

A library may list no quality profile and still get good results: with a target and an empty profile list its releases are
ranked by the target alone (`judge.TargetOnly`: the target's resolution, codec, channels and numbers first, then the source;
only xvid and divx are refused). With neither a target nor a profile the instance default profile applies as before.

A library may also aim for **Automatic**: the average of its first quality profile (`settings.AutoTarget`, migration 0068).
For each kind of thing it takes what the profile ranks first (resolution, codec, HDR, channels, file type) and for each number
the middle of the range (its ideal, else the mean of minimum and maximum); a lossless first codec has no bitrate. It follows the
profile as it is edited and needs a profile. It is not a stored target, so it is neither listed under Targets nor exported
as one (the export keeps `quality_target_auto`).

## The target is the goal; profiles are for experts

Settings > **Quality** (was Quality profiles) opens on **Target qualities**; Content profiles show at the advanced tier and
Quality profiles at the expert one. Each library's target is chosen right in Settings > Libraries, and on the library's own
Quality page the target comes first and the profile list below it, "none" by default. A library added as a kind starts with its
media type's preferred ("Space conscious") target and no profile; a type with no shipped target (comics, ROMs) still starts with the
kind's profiles. For the others the kind's profiles (Anime 1080p) are offered as one-click "Suggested for this kind of library" buttons in the expert profile section of the library's Quality page.

## Families, names and tiles

Target qualities belong to a **family** of media types, not to one type (migration 0069, `settings.TargetFamily`): **video**
(movies, series, adult, YouTube, news), **music**, **spoken** (audiobooks, podcasts) and **book** (books, comics). A movie, an
episode and a scene are the same kind of file, so one preset serves them all and each library still picks its own. A target made
for `tv` is stored in the video family; content profiles name families too. Under Settings > Quality > Target qualities each family
is a collapsible group of tiles: the short name (label) is the title and the specification the small line beneath, and a library
choice lists targets by that short name.

A constant-quality encode whose **CRF is within the target's tolerance** is the quality the target asks for at any picture bitrate: a
small or quiet picture (a low-resolution source) needs far fewer bits, so the video bitrate floor does not apply to it and its
closeness counts the bitrate as met (an audio bitrate still has its floor). Without a stated CRF the bitrate floors above apply.
