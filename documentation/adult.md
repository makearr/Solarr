# Adult

> **Level: 🟡 Advanced.** The adult media type.

The adult media type is Whisparr's job in Solarr: scenes and films, their performers and studios, over `internal/media/works`
like movies and books. One type, two library kinds: **Scenes** (the unit Stash and Whisparr keep) and **Movies** (whole
films). Package `internal/media/adult`; the file and release name parser is `internal/identify/parseadult`.

## Providers

A library asks its providers in its own order (Settings, Metadata). Default: Wikidata, Whisparr's server, StashDB, ThePornDB,
FansDB. Each answers by the ids known, else by the scene's name; the first supplies a field and the later ones fill what is
missing (ADR-8). Credits are one source's: a performer has a different id at each server, so mixing two lists would credit
them twice. Details of each provider: [metadata-providers.md](metadata-providers.md) (Adult).

Films come from ThePornDB's REST API (`internal/metadata/theporndb`, `/movies`, the same key): the stash-box servers and
Whisparr's server hold scenes only, so a Movies library finds its films only there. They are stored as scenes with the
film's site as studio.

A film a provider holds only as numbered scenes ("Title - Scene 1", "Title - Scene 2") has no record of its own. A whole file of
that title is never placed on one scene: Review offers "Title: the whole film of N scenes" first, and picking it builds one work
(`adult/film.go`) with the film's title, everyone who appears in it and their length added up. Every scene's id stays on the work,
so a release or fingerprint naming a scene finds the film; the `adult:film` id marks it, and no refresh renames it.

The three stash-box servers are one client, `internal/metadata/stashbox`. Their keys are the owner's own, sealed in
`settings.db`, never shipped. ThePornDB answers `searchScene` as the others do but not the `queryScenes` filter, so
following a performer or studio uses StashDB and FansDB.

## Identification

A scan registers each video file with its oshash (and the quick hash), then resolves it in this order:

1. **Fingerprint**: a server that knows the oshash names the scene (Stash's own method).
2. **A scene the library holds** under that title, in an agreeing year; a file with a day skips this step, since a studio's
   scenes of one title (`Title #04`) come on twenty days and only the day tells them apart.
3. **A search** by studio and title. A file with a day asks the stash-box servers for the scenes of that day under the title
   first (`queryScenes` with a date filter and the title as *text*, which reads through the punctuation, accents and `...` cuts
   that file names change; ten times quicker than their search); a batch's days go in one request of twenty. A day answered
   there skips that server's text search, which a second pass runs only when no scene was placed. The scene taken is the one of that title **on that day, wherever the
   provider ranks it**; among several (one title, one day) the file's studio, then the performers its name ends in, then
   its measured length (within 3 s or 2 %) choose. A title that only continues the file's (`Title, Scene #01`) counts only when no plain one
   is listed. A day or two off bridges only a lone identical title, never when the providers list that title on other days.
   Only an exact pick stops the search at a provider; a looser one lets the next provider answer.
4. Anything less clear waits in **Review** with the candidates, as for every type.

Names read: `[Studio] Title (2020-05-14).mp4` below a studio folder, `Title (2018).mp4`, a trailing `- Performer`, and a scene
group's `Studio.20.05.14.Performer.Title.1080p`. Its phash is computed like any video's (`duplicate-videos.md`).

**What a scan costs the providers.** A library of one file per folder is gathered into batches of 40 (`scan.Sweep.GatherFiles`),
whose oshashes go to each server in one query, and eight files of a batch are placed side by side (a file waits on the providers
far longer than it works; each provider's own rate bucket sets the pace). A title search stops at the first provider that settles it, except in a movies
library, where a later provider may hold the film's numbered scenes. Whisparr's server names a site by number, so the studio comes from its site lookup under the file's studio. Adding a scene from a scan takes the scene a provider listed as that provider's answer and asks providers in the library's
order only until the scene holds a title, day, studio, a performer and a picture; the remaining providers fill the rest at the
scene's refresh. Searches are remembered for an hour, empty ones too (`searchMemo`), a provider keeps a lookup at most two
minutes, and Whisparr's search gets at most 80 characters, the first clause of a long name, and a 10-second timeout.
A slice of the scan ending mid-lookup (the providers are cancelled with it) re-reads the batch rather than queueing it for Review.

## What is stored

A scene is a work with one wanted item. Its studio is an organisation of kind `studio` (its network beside it), its performers
are credits in the `performer` role, its tags are tags of the `adult` tree (every provider tag becomes a tag), and its release
day is the work's `release` date, so the calendar and "not out yet" work as for films.

Performers are people with extra facts and attributes (height, hair, ethnicity, measurements, tattoos, piercings, status), which the people list filters on and a person's own edits stand over. `adult.performers` (the *Refresh people* schedule) reads each performer from the
servers that know them and fills gender, country, birth and death, aliases, a picture, and the page's facts (ethnicity, hair,
eyes, height, measurements, breasts, career, tattoos, piercings), links and a picture gallery. Core keeps these as
`library.PersonExtras` in `person.fields_json` without knowing what they mean (ADR-23). A biography comes from Wikidata's
Wikipedia lead when a server links the performer to an item.

## Adding

The add dialog takes **Scenes** (looked up by title, "[Studio] Title" narrowing it to a studio) and **Performers**. Adding a
performer searches the servers in the library's order, makes the person with everything the server says of them and, when
monitored, follows them back to their first scene (Whisparr's "Add performer"). Studios are followed from a studio's own page.

A performer's page lists, under *Also known for*, their newest scenes (up to 60) at the first server that knows them which the
library lacks, each with *Add to Solarr* (`adult.PerformerScenes`, the adult type's `media.Profiling`; ThePornDB cannot list them).

## Following

Following a performer or a studio (Whisparr's monitored performers and studios) adds the scenes a server lists for them: those
out since the follow was made (two weeks earlier), or the whole back catalogue, five at a time. Added scenes are monitored.

## Import list

*Stash-box favourites* adds the newest scenes of the performers and studios you favourited at StashDB or FansDB (the account
your API key belongs to), as Whisparr's StashDB list does. It reads the newest 50 to 500 scenes each time and skips what is
held. [import-lists.md](import-lists.md).

## Acquisition

Newznab's XXX categories (6000-6090), by text. Queries try the studio and day a scene group names a release by
(`studio yy mm dd`), then studio and title, then the first performer and title. A dated release is the scene when its day
matches and its studio (written without spaces by the groups), title or a performer matches. Quality is judged as for movies
(resolution, source, codec, measured bitrate), with three shipped profiles: *Adult: 1080p preferred*, *720p to 1080p*, *any
quality*. RSS, hunting, upgrades and the cutoff check are the shared ones.

## Naming

Slots *Studio Folder* and *Standard Scene*. The default, `[{Network Name}/]{Studio Name}` (the studio below its network when it has one; `{Studio Network}` is the same token) then
`{Scene Title} ({Release Date})/[{Studio Name}] {Scene Title} ({Release Date})`, is the layout Stash users keep; a Whisparr
preset and one with performers follow. Tokens: studio, network, scene title, code, release date and year, performer names,
release group, original filename, StashDB id, part number.

## Not built yet

phash, fingerprint submission, picture galleries as a library kind, ThePornDB's import lists. An import from a Whisparr
instance is [arr-import.md](arr-import.md).
