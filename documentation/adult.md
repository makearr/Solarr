# Adult

The adult media type is Whisparr's job in Solarr: scenes and films, their performers and studios, over `internal/media/works`
like movies and books. One type, two library kinds: **Scenes** (the unit Stash and Whisparr keep) and **Movies** (whole
films). Package `internal/media/adult`; the file and release name parser is `internal/identify/parseadult`.

## Providers

A library asks its providers in its own order (Settings, Metadata). Default: Wikidata, Whisparr's server, StashDB, ThePornDB,
FansDB. Each answers by the ids known, else by the scene's name; the first supplies a field and the later ones fill what is
missing (ADR-8). Credits are one source's: a performer has a different id at each server, so mixing two lists would credit
them twice. Details of each provider: [metadata-providers.md](metadata-providers.md) (Adult).

The three stash-box servers are one client, `internal/metadata/stashbox`. Their keys are the owner's own, sealed in
`settings.db`, never shipped. ThePornDB answers `searchScene` as the others do but not the `queryScenes` filter, so
following a performer or studio uses StashDB and FansDB.

## Identification

A scan registers each video file with its oshash (and the quick hash), then resolves it in this order:

1. **Fingerprint**: a server that knows the oshash names the scene (Stash's own method).
2. **A scene the library holds** under that title, in an agreeing year.
3. **A search** by studio and title: the one hit of that title (and, where both know the day, that day) in the provider's
   top three is taken and added. `Title` and `Title - Scene 3` count as one on the same day; a scene a network lists under
   two sites is one scene.
4. Anything less clear waits in **Review** with the candidates, as for every type.

Names read: `[Studio] Title (2020-05-14).mp4` below a studio folder, `Title (2018).mp4`, a trailing `- Performer`, and a scene
group's `Studio.20.05.14.Performer.Title.1080p`. phash is not computed yet.

## What is stored

A scene is a work with one wanted item. Its studio is an organisation of kind `studio` (its network beside it), its performers
are credits in the `performer` role, its tags are tags of the `adult` tree (every provider tag becomes a tag), and its release
day is the work's `release` date, so the calendar and "not out yet" work as for films.

Performers are people with extra facts. `adult.performers` (the *Refresh people* schedule) reads each performer from the
servers that know them and fills gender, country, birth and death, aliases, a picture, and the page's facts (ethnicity, hair,
eyes, height, measurements, breasts, career, tattoos, piercings), links and a picture gallery. Core keeps these as
`library.PersonExtras` in `person.fields_json` without knowing what they mean (ADR-23). A biography comes from Wikidata's
Wikipedia lead when a server links the performer to an item.

## Following

Following a performer or a studio (Whisparr's monitored performers and studios) adds the scenes a server lists for them: those
out since the follow was made (two weeks earlier), or the whole back catalogue, five at a time. Added scenes are monitored.

## Acquisition

Newznab's XXX categories (6000-6090), by text. Queries try the studio and day a scene group names a release by
(`studio yy mm dd`), then studio and title, then the first performer and title. A dated release is the scene when its day
matches and its studio (written without spaces by the groups), title or a performer matches. Quality is judged as for movies
(resolution, source, codec, measured bitrate), with three shipped profiles: *Adult: 1080p preferred*, *720p to 1080p*, *any
quality*. RSS, hunting, upgrades and the cutoff check are the shared ones.

## Naming

Slots *Studio Folder* and *Standard Scene*. The default, `{Studio Name}` then
`{Scene Title} ({Release Date})/[{Studio Name}] {Scene Title} ({Release Date})`, is the layout Stash users keep; a Whisparr
preset and one with performers follow. Tokens: studio, network, scene title, code, release date and year, performer names,
release group, original filename, StashDB id, part number.

## Not built yet

phash, fingerprint submission, picture galleries as a library kind, an import from a Whisparr instance, ThePornDB's import
lists.
