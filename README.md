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
upgrades when something better shows up. It doesn't fire searches on a timer and hope; it keeps
a running picture of what's still outstanding and works through it at a pace your indexers and
download client can sustain.

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

### Understands editions and versions

The 1977 theatrical cut, the 1997 Special Edition and the 2004 remaster are different things,
not duplicates of each other. Same for album remasters with different track listings, and
director's cuts. Keep one, keep several — and never have a cleanup tool delete the wrong one.

### Knows when one file belongs in two places

A crossover episode that's canonically in two series. A track credited to two artists. One file
on disk, appearing correctly in both places, counted once. Duplicate detection understands the
difference between "the same file in two libraries" and "an actual duplicate".

### Never loses track of what it downloaded

You search for *The Lord of the Rings: The Fellowship of the Ring*, the release is called
`LOTR.2001.EXTENDED.2160p.REMUX-GROUP`, and it lands as a properly named file. MediaNexus
records the whole chain, so identity never depends on guessing from a filename.

### Handles a library across several disks

A category can span as many drives as you like. Related content stays together — a series or
an artist's discography lands on one disk rather than being scattered — and you choose how new
content is distributed: most free space, fill one drive at a time, proportional weighting, or
by hand. Retiring a drive is a setting, not a migration.

### Organises the way you want

Every media type and every category gets its own folder and its own rules. Anime and regular
series can live on different drives, with different naming, different indexers, and different
language preferences — Japanese audio with English subtitles for one, English audio for the
other.

### Music, done properly

MediaNexus was built music-first, because music is the hardest case. Album completeness is
tracked track by track. Existing tags are read and trusted, never trampled — it fills gaps
rather than overwriting the work you've already done. Collaborations, compilations, multi-disc
releases and classical credits all work.

### Doesn't get in the way

The interface stays responsive no matter what's running in the background. Large imports,
metadata refreshes and library scans are jobs you can watch, not spinners you wait on. A
broken indexer doesn't stop the others; a failing plugin doesn't take the application down.

### Tells you when it's doing less

If MediaNexus slows down, it says so, in plain terms:

```
⚠ Hunt searches paused
  Queue at 47 items (limit 40). Resumes below 40.
  New releases from RSS are still being acquired.
```

---

## Made to be usable

Three interface levels — **normal**, **advanced** and **expert** — so the settings you need are
visible and the ones you don't are out of the way until you ask for them.

Multi-user, with per-library visibility: different people can see different parts of the
collection.

---

## Moving from Radarr, Sonarr or Lidarr

Point MediaNexus at your existing setup and it imports what's monitored, your quality
profiles, indexers and download clients. Run it in observe mode to see what it *would* do
without touching anything, then in shadow mode to compare its decisions against your current
setup side by side, with every difference explained. Switch over when you're satisfied. Your
existing installation is never modified.

---

## Running it

A single Docker container. One binary with the web interface built in — no external database,
no message broker, no cache server to run alongside it.

---

## Status

In development, pre-release. Not yet usable.

## Licence

[AGPL-3.0-or-later](LICENSE). Free to use, modify and self-host. If you run a modified version
as a service for others, you must make your changes available to them.
