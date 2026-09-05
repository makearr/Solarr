# MediaNexus

One application for your entire media library — music, TV, movies, audiobooks, books, comics
and more. It works out what you want, what you have, what's missing, and goes and gets it.

MediaNexus replaces Radarr, Sonarr and Lidarr with a single engine, and can run alongside them
while you decide whether to switch.

---

## What it does

### Finds what's missing, continuously

You describe the library you want. MediaNexus works out the difference between that and what's
on disk, and closes the gap — new releases as they appear, gaps in old collections, and
upgrades when something better shows up. It keeps a running picture of what's still outstanding
and works through it at a pace your indexers and download client can sustain.

**New releases come first by default.** A film released last week matters more than a gap in a
collection from 2009, and that's how the queue is ordered — adjustable if you disagree, and
built so nothing at the bottom of the list waits forever.

### Point it at a folder — any folder

Give it a path and it works out what's in there. Content three levels down, several unrelated
artists in one directory, an album split across `CD1` and `CD2`, a discography whose folders
sit two levels deep — all handled. Folder names are read as clues, so
`Artist/Album (Year)/track.mp3` identifies correctly even when the files themselves are
untagged.

You are never asked to tidy up first, or to point at one album at a time.

### Handles packs, box sets and archives

A discography, a multi-season pack or a box set arrives as one thing and becomes many correctly
named albums or seasons in the right places — each identified on its own, while the download
stays a single unit for verification and seeding. A pack that covers five seasons of an
eight-season show closes five gaps and leaves the other three open, visibly.

Packed content is opened for you — zip, rar, 7z, tar, including multi-part sets. Archives
already sitting in your library are read rather than unpacked, so an album stored as a `.rar`
counts as present instead of showing up as something to download again.

### Keeps fakes and junk out

Files that are obviously not media — executables, scripts, shortcuts, checksum files — are
recognised for what they are. An `.exe` inside a music release doesn't get quietly stepped over;
it stops the import and tells you, because that's usually the most useful thing in the folder.
The list is yours to edit.

And when something turns out to be a fake, **remove and block** does the whole job in one
action: the file goes to quarantine, its *contents* are blocked so those exact bytes can never
be imported again — blocking by name would be defeated by a rename — the release it came from is
blocklisted so it isn't grabbed a second time, and what you actually wanted goes back on the
list to be found properly. Any other copies of the same file in your library are cleared out
with it.

### Explains every decision

Any time MediaNexus does something — or doesn't — you can ask why and get a real answer:

```
Cige: Part Two (2024)                                          WAITING

  ✓ Monitored                    ✓ Upgrade threshold met (+31, min 15)
  ✓ Available since 2024-03-01   ✗ Downloader queue full (47/40)
  ✓ Found 2160p REMUX, score 94  ✗ Est. 4h 10m at current speed

  +30 2160p   +25 disc source   +20 HEVC   +10 TrueHD   −15 slow
```

No log spelunking. Every rejected release keeps its reasons, so "why didn't this download?"
is a question with an answer, days later.

### Judges quality on what a file *is*

The release name is a claim; the file itself is the evidence, and the evidence wins. A release
labelled 2160p that turns out to be an upscale doesn't get treated as an upgrade. Neither does a
bigger number for its own sake — a well-encoded 48kHz file can beat a bloated 192kHz one if
that's what you asked for, and an "upgrade" that isn't actually better is simply not made.

### Understands editions and versions

The 1977 theatrical cut, the 1997 Special Edition and the 2004 remaster are different things,
not duplicates of each other. Same for album remasters with different track listings, and
director's cuts. Keep one, keep several — and never have a cleanup tool delete the wrong one.

### Knows when one file belongs in two places

A crossover episode that's canonically in two series. A track credited to two artists. One file
on disk, appearing correctly in both places, counted once — not flagged as a duplicate.

### Handles a library across several disks

A category can span as many drives as you like. Related content stays together — a series or an
artist's discography lands on one disk rather than being scattered — and you choose how new
content is distributed: most free space, fill one drive at a time, proportional weighting, or by
hand.

Retiring a drive is a button. Mark it for draining and MediaNexus moves everything off it onto
your other disks, keeping artists and series intact, showing you what it will do before it
starts, and telling you up front if there isn't enough room rather than filling the others and
giving up half way.

### Organises the way you want

Every media type and every category gets its own folder and its own rules. Anime and regular
series can live on different drives, with different naming, different indexers, and different
language preferences — Japanese audio with English subtitles for one, English audio for the
other.

Renaming shows you the exact changes as a list before anything moves, and the shipped naming
matches what Radarr, Sonarr and Lidarr already produce — so adopting an existing library
proposes no renames at all.

### Music, done properly

MediaNexus was built music-first, because music is the hardest case. Album completeness is
tracked track by track. Existing tags are read and trusted, never trampled — it fills gaps
rather than overwriting the work you've already done. Collaborations, compilations, multi-disc
releases and classical credits all work.

### Tells you when it's doing less

If MediaNexus slows down, it says so, in plain terms:

```
⚠ Hunt searches paused
  Queue at 47 items (limit 40). Resumes below 40.
  New releases from RSS are still being acquired.
```

---

## Fast, and one thing to run

MediaNexus is written in Go and ships as a single binary with the web interface built into it —
nothing to install alongside it, no database server, no message broker, no cache. It's built to
stay quick with hundreds of thousands of files: lists stay smooth however far you scroll, counts
appear instantly instead of being recalculated every time you look at them, and long jobs like
imports and library scans run in the background where you can watch them rather than behind a
spinner you have to wait on. Speed targets are measured on every change, so "it got slower" is
something the project catches rather than something you report.

---

## Made to be usable

Three interface levels — **normal**, **advanced** and **expert** — so the settings you need are
visible and the ones you don't stay out of the way until you ask for them. Nothing is hidden;
search finds every setting at any level.

Multi-user, with per-library visibility: different people can see different parts of the
collection.

---

## Moving from Radarr, Sonarr, Lidarr and friends

Connect MediaNexus to your existing setup — Sonarr, Radarr, Lidarr, Readarr, Whisparr or
Prowlarr — and it imports what's monitored, along with your root folders, quality profiles,
indexers and download clients. You see exactly what it will bring across before it does, and
anything that doesn't translate cleanly is shown to you rather than silently guessed at.

Run it in observe mode to see what it *would* do without touching anything, then in shadow mode
to compare its decisions against your current setup side by side, with every difference
explained. Switch over when you're satisfied. Your existing installation is never modified.

---

## Running it

A single Docker container, one volume for configuration and your library mounted where you
already keep it.

---

## Status

In development, pre-release. Not yet usable.

## Licence

[AGPL-3.0-or-later](LICENSE). Free to use, modify and self-host. If you run a modified version
as a service for others, you must make your changes available to them.
