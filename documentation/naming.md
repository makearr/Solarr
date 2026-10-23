# Naming

How a library names its files: Lidarr's model, per library (the 2026-09-19 review).

## Shape

A media type declares its naming as data (`naming.Scheme`, ADR-23): **slots** (music: Artist
Folder Format, Standard Track Format, Multi Disc Track Format), the **tokens** they may use with
the help text the token builder shows, and named **presets**, the first being the default.
A library owns one `naming_profile` (settings.db, created on first save) with a template per slot
in `naming_format`; a library never saved uses the default. Validation happens on write, through
the media type's scheme, for the interface and for a YAML restore alike.

Music renders `artist_folder + "/" + track format`, the multi-disc format when a release has
more than one disc. A `/` in a template starts a folder; a `/` inside a value never does.

## Characters

Applied to every path component after rendering, **template literals included** (the user's
answer, 2026-09-24):

| Replace Illegal Characters on | off |
|---|---|
| `\` `/` → `+`, `?` → `!`, `*` → `-`, `< > \| "` removed | all of those removed |
| `:` per Colon Replacement: delete, `-`, ` -`, ` - `, or smart (`": "` → `" - "`, `":"` → `"-"`) | `:` removed |

Then a doubled separator (`  `, `--`, `..`, `__`) becomes one and trailing separators go —
what an empty token leaves behind. The default multi-disc template writes `{medium:0}-{track:00}`
(`2-05`) itself since 2026-09-25, so no colon reaches the replacement.

These are exactly the characters an SMB share turns into 8.3 names (`C*Bomlbor`, a trailing dot
as in `Cibiz M.`); symbols such as `★` are valid there and kept.

## Token spellings

As Lidarr: `{Artist.Name}` / `{Artist_Name}` / `{Artist-Name}` put that separator between the
value's words; `{ARTIST NAME}` and `{artist name}` change its case. One-word tokens (`{track}`,
`{medium}`) are never re-cased. `{track:00}` pads; `{Track Title:.30}` truncates.

## Rename Tracks

Off, a file keeps the name it arrived with and still goes into the rendered folders. The Import
screen's rename checkbox can only turn renaming off for one import, never override a library.

## Extra files

Lidarr's and Sonarr's "Import Extra Files", per library under File management -> Importing: each media type
declares its own default (`media.Descriptor.ExtraFiles`): music off with `lrc,cue,log,nfo`, TV on with `srt`.
Only files inspect did not class as media are considered. A file whose name matches a track's
(case-insensitive, without extension) takes that track's new name with its own extension; any
other goes to the folder the tracks beside it went to, under its own name. An existing file is
never overwritten; a skipped extra is logged and never fails the album. Extras are recorded in
`import_unit_extra`, so reverting the session recycles them with the tracks.

## Apply to library

`POST /categories/{id}/rename` runs one rename per root the library uses, through the move
engine (ADR-51), touching only that library's works. Preview first; read-only roots refuse unless
they allow renaming. Music, TV and movies share one engine (`renamejob.Engine`); the media type only names a
work's files. A file two works hold (a crossover episode) stays where it is.

Files that are no media move with their file in the same item (`move_item_companion`): one named like it plus an
ending (`.en.srt`, `.nfo`, `-thumb.jpg`, `.lrc`) takes the new name with that ending; when every media file of a
folder moves to one new folder (an album, a season, a movie, and the artist or series folder above), the folder's
other files (`cover.jpg`, `tvshow.nfo`) go with it. An audio or video file is never a companion, and a companion
whose new name is taken stays where it is.
