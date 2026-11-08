# People, labels, networks and following

> **Level: 🟢 Beginner** to browse; 🟡 **Advanced** to edit and merge people.

## One person, one page (🟢)

Artists, Cast and the **People** entry in the sidebar (everyone from every library you may read) are one list:
Posters, Overview and Table views, role chips, sorted by name, number of works, newest work or when added,
either way.

A person has one page wherever you open them:

- who they are;
- their works in each of your libraries, shown as that library shows them (an artist's albums by release type,
  an actor's series as posters with the parts they played);
- what TMDB also knows them for (an **Also known for** title opens Solarr's add panel, or the series when the
  library already has it; **Add to Solarr**);
- **Follow** for each library they could be followed into;
- the works can be ordered by title, year or availability, and a tag on the page opens the library's list of
  everything tagged with it.

Networks and labels work the same way. A music library's **Labels** level lists the labels of its albums (from
MusicBrainz, filled in by the hourly metadata run; their logos come from Wikidata, Settings → Scheduler → Find
label logos).

## Follow (🟢)

**Follow** (on a person's, network's or label's page) adds their new works to a library for you, one follow per
library.

| Follow | Adds |
|---|---|
| An artist | Albums of the release types you tick |
| An actor or director | New series by the roles you tick (needs your TMDB key) |
| A network | Its new series |
| A label | What it releases from then on, or its back catalogue, up to 25 albums a run, with the release types you tick |

- A TV follow adds at most five series each time it runs, with the monitor choice you set. Older works come only
  if you ask for the back catalogue.
- Before you follow, the panel lists the series it would add right now (and how many already are in your
  library), so nothing arrives unseen.

## The same person in more than one library (🟡)

An actor who is also a musician in your music library is one person. Solarr recognises them through their
Wikidata item, never by name alone: the task **Match people across libraries** asks TMDB for the item of TV
people who share a name with a music artist, and a refreshed artist records theirs. A name that differs only in
punctuation (a quoted nickname) is also tried. Their TV page then shows **Also known for** (their
albums), with a link to the full profile. An actor who also records lists their MusicBrainz studio albums and
EPs there too (found through Wikidata), added to the music library.

## Editing people (🟡, administrators)

On a person's page: **Edit** the name, gender and attributes (nationality, height, weight, measurements, hair
colour, ethnicity, status, tattoos, piercings; a provider's values fill in what is empty, yours stay), and
**Merge into…** another person found by name. The people list filters on all of those.

## One person, many sources

A person two sources know under different ids (a performer on StashDB and on ThePornDB, an actor on TMDB and TVDB, an
artist who also acts) is one person. A new id joins the identified person of the same name, or whose alias is that
name, when that person holds no id from the same source and their births do not differ; two candidates stay apart. The
daily **Merge duplicate people** action does the same for people already stored. Where a name is shared and the
sources disagree, only people of one known birth (date, else year) merge; people with no birth stay apart.

Opening a person who has no biography, birth or picture reads their details at once, ahead of the background sweeps
(at most once a day per person); the page looks again a few times while it is read.
