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
what an empty token leaves behind. Smart replacement is why the default multi-disc
`{medium:0}:{track:00}` renders `2-05`.

## Token spellings

As Lidarr: `{Artist.Name}` / `{Artist_Name}` / `{Artist-Name}` put that separator between the
value's words; `{ARTIST NAME}` and `{artist name}` change its case. One-word tokens (`{track}`,
`{medium}`) are never re-cased. `{track:00}` pads; `{Track Title:.30}` truncates.

## Rename Tracks

Off, a file keeps the name it arrived with and still goes into the rendered folders. The Import
screen's rename checkbox can only turn renaming off for one import, never override a library.

## Apply to library

`POST /categories/{id}/rename` runs one rename per root the library uses, through the move
engine (ADR-51), touching only that library's works. Preview first; read-only roots refuse unless
they allow renaming.
