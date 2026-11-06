# Import a folder

> **Level: 🟡 Advanced**, administrators. For connecting a Lidarr/Sonarr/Radarr instead, see
> [arr-import.md](../arr-import.md).

**Storage → Import**: point at any folder (one album, a whole collection, a messy download folder). Solarr works
out what is in it. Every import can be **reverted** from its session.

## The options (🟡)

| Option | Effect |
|---|---|
| **Preview first** | Places nothing until you commit from the review |
| **Library** | Limits the import to one library; *Decide later* inspects and places nothing |
| **Add as a root instead of moving** | Makes the folder itself a root of that library, scanned where it is, instead of moving files out of it |
| **Add unrecognised albums** (off by default) | A folder no metadata source recognises still becomes an album, named by its tags or folder and marked **Unverified**. It is not searched for; commit it from the review with the right candidate and its files move onto the real album |
| **Import extra files** (each library's **File management → Importing**; on for TV, off for music) | Brings files with the listed extensions along: lyrics, cue sheets and logs beside an album, and in a TV library the subtitles named after an episode (`Show.S01E02.en.srt` becomes `Show - S01E02 - Title.en.srt`). Nothing already there is overwritten |

## Archives (🟡)

A zip, rar or 7z (multipart sets as one, archives inside archives too) is opened into a workspace under the
cache folder, and its contents are inspected and committed exactly like a folder. A download that is only an
archive imports the same way.

A folder you pointed at keeps its archives until everything in them is in the library, then they move into a
`.solarr-recycle` folder inside it (deleted after a week by the next import of that folder). A read-only source
is never touched.

## A folder held for review (🟡)

- **Commit anyway** places it as its best match.
- **Identify as…**: search for the album or series the files are, pick it, and they are placed as that. This is
  also the way out for a folder nothing could identify.

Both need the session to have a library.

In a TV library the episodes are read from the file names (`S01E02`, an air date, an absolute number). A series
the library does not hold yet is added first, and a file for an episode the library already has is left where it
is. **Commit anyway** on a held TV folder places it as its best guess only when that guess is at least 75% sure;
below that, name the series with **Identify as…** (a folder named by hand is easily guessed wrong).

## Files a scan could not identify (🟢)

They wait under the library's **Unidentified** page (**Library → Music → Unidentified**, or the TV equivalent in
[tv.md](tv.md#unidentified-files-)). Solarr asks again every night.

## Rules and internals (🔴)

[import-rules.md](../import-rules.md), [music-identification.md](../music-identification.md).
