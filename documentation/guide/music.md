# Music

> **Level: 🟢 Beginner** for adding and following music. Sections marked 🟡 or 🔴 need the *advanced* or *expert*
> interface level (Settings → User).

## Add music (🟢)

**Library → Music → Add new** (or Discover): search for an album or an artist.

- **Monitor what I add**: whether Solarr should get it. Off keeps it in the library without looking for it.
- **Start search for missing albums** (on by default, remembered for you): an album is searched at once; an
  artist's albums are added straight away and their missing ones searched.

Monitoring an album later from its own page also searches it at once while that box is ticked.

## Find your way around (🟢)

| Where | What |
|---|---|
| **Library → Music** | Albums as posters, an overview or a table. Sort by title, artist, release, added, size on disk, quality profile, status, genres or tags |
| **Tracks** | Every track, album by album. The search box finds a track by title, album or artist; **Filter** narrows by title, album, artist, year, monitored or whether it has a file |
| **Tags** button | Narrows to works carrying a tag. A genre includes everything below it (Rock finds Metal). A click on a chip turns it into "not"; several chips must all hold |
| **Fresh** | Released within a week, or shown by an RSS feed within 48 hours whatever the release date (a manual search never counts). Fresh albums are also searched first |
| **Unidentified** | Files a library scan could not identify |

## Edit an album (🟢)

**Edit** (the pencil on an album's poster or row, as in Radarr) sets:

- **Monitored.**
- The album's **own quality profile**, used instead of the library's profiles when it is searched or upgraded.
  The library's cutoff still decides whether it counts as upgradable.
- Your **tags**. Typing suggests the library's genres and your own labels; a name no tag has becomes a new label.
  Tags a metadata provider assigned are never touched.
- 🟡 In a library with several root folders, the **root folder**. Choosing another shows how many files move and
  moves them on Save (all or none, through the same copy-verify-delete as every move); later downloads follow.

With albums selected (Table view, or **Select all matching**, which respects the Fresh filter, for Monitor and
Unmonitor too), **Edit** in the toolbar applies only what you change to all of them, after showing the count.

## Discover (🟢)

With an empty search box, Discover lists **artists you have albums of, but not all**: your library compared with
each artist's discography (live albums and compilations left out), the most missing first, with one click to
follow the artist and add the rest. It fills in after the weekly comparison has run (Settings → Scheduler →
Compare discographies).

## Pictures (🟢)

On an album's, series' or artist's page, **Choose image** lists the pictures the metadata providers hold (Cover
Art Archive, TheAudioDB, Wikidata; a series' posters from its TV sources). The one you pick is kept over later
refreshes.

## Duplicates (🟡)

**Library → Duplicates** lists tracks held more than once, found by artist, title and length (a flac and a
96 kbit mp3 of one song, on any albums), and recommends the copy closer to the library's target quality.

You choose the copy to keep (a radio per copy) and, for each other copy, one of:

- its paths **become links** to the kept file (hardlink or reflink; the other album keeps its track, the old
  file waits seven days in the recycle bin; only between files of the same extension), or
- the copy **goes to the recycle bin**.

A confirmation lists every path before anything changes.

**Identical files** (above it) covers the very same bytes stored at two paths: a preview of which second paths
can become a reflink (else a hardlink) of the first, per pair on one filesystem, and a **Link identical files**
button (manage access) that compares every byte again before swapping a path atomically.

Roots that are offline, not writable or read-only are never touched.

## An album held twice (🟡)

The same release found under two albums (for example after MusicBrainz merged two release groups) shows a
warning on its page naming the other one. **Merge into it…** shows what moves; **Merge** moves its files, wanted
state, downloads and history onto the other album, keeps its name and old ids as the other's aliases, and removes
it.

## File management and quality (🟡)

- **Library → Music → File management**: **Merge Artists Stored Under Several Names** folds duplicate artists
  into one after each disk scan; **Navidrome profile** (under naming → tags) also fills the track artist's tags
  and puts a `cover.jpg` in each album folder that has none. See [naming.md](../naming.md) and
  [file-management.md](../file-management.md).
- **Library → Music → Quality** and **Quality profiles**: a profile ranks codecs, file extensions and
  containers (an `.m4a` and an `.mp4` are both MP4), channels and bitrate targets. See
  [library-quality.md](../library-quality.md).

## Statistics (🟢)

**Library → Statistics**: monitored albums by state (each figure opens those albums), files and bytes by audio
codec, growth by month (when Solarr first saw each file), the last 30 days' release decisions and downloads, and
the 20 biggest albums. Computed once a day, not live.

## Under the hood (🔴)

How a file becomes an album: [music-identification.md](../music-identification.md).
