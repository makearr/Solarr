# Downloads: what is sent, and what waits

## Missing albums are searched on their own

Two scheduled tasks (Settings -> Scheduler) do what Lidarr leaves to a person pressing *Search all*:

- **Plan wanted albums** (every 5 minutes, advanced) reads every album whose state or search record
  changed, plus a slice of the rest so a whole pass takes a day, and writes the hunt queue: which
  monitored MISSING or PARTIAL albums may be searched, how urgently (the priority ladder below), and
  from when. An album with a known street date waits until 12 hours after it; one just searched
  waits 6 hours, then 12, 24, doubling to a week. Tracks are never searched on their own.
- **Search for missing albums** (every 15 minutes) searches the most urgent eligible albums, least
  recently searched first, and sends the best release as an automatic grab. It spends at most
  **Automatic searches per hour** (Settings -> Priority, default 12, 0 = off); an album with a
  download already on its way costs nothing and is looked at again in an hour. Every search counts
  toward the backoff, whoever started it.

**RSS sync** (every 15 minutes, as Lidarr's) reads each indexer's newest 100 audio releases
(Newznab category 3000 -- without it a feed is mostly television), groups them by the artist they
name, and judges each group against that artist's wanted albums exactly as a search would. The best
accepted release is sent as an RSS grab. Nothing is searched, and an album's search backoff is left
alone. Artists are matched by name ignoring case; an alias or a differently spelled name is not yet
matched.

Every search asks all enabled indexers at once and waits at most **20 seconds** for them (ADR-37):
a dead or slow indexer bounds a search instead of adding to it. The album page and Interactive
search name each indexer that failed or did not answer in time.

Adding from **Discover** with **Start search for missing albums** ticked (on by default, remembered
per person) searches an album at once, as Lidarr does. For an artist it adds their albums now rather
than at the 12-hourly subscription expansion, then searches the missing ones. Monitoring an album
from its page does not search; the hunt picks it up within 15 minutes.

Once a release is in a download client, the client decides the order. So Solarr sends each client
only as many releases as its **Downloads at once** (per client, default 10; ADR-57) and keeps the
rest **held**, in its own order, until a download finishes or is removed.

## Who asked decides whether a release waits

| Grab | Examples | Full client |
|---|---|---|
| **Yours** | an album's *Search*, a release picked in *Interactive search*, a request | sent anyway, unless the client's **Always send my grabs** is off |
| **RSS** | a release an RSS sync found for a wanted album (M1c) | sent anyway, unless **Always send RSS grabs** is off |
| **Automatic** | Wanted's *Search all*, the search after a failed download, the planner (M1c) | held |

Both boxes are on by default (2026-09-29). An interactive pick counts as yours.

## The order held releases go in

Your grabs first, then RSS, then new albums (released within 7 days, or of unknown date), then
older ones (ADR-36's ladder: `priority.Compute` with the user-request, RSS, new-release and gap
weights). Within one, the longest waiting goes first. Each is checked again just before it is
sent: still monitored, not blocklisted, nothing already on its way for the album; otherwise it is
dropped and the album searched afresh later. One release per album is held; a newer decision
replaces it.

**Activity → Queue** lists the held releases below the downloads, in send order, with their size,
who asked, the indexer and how long they have waited. The album page says "Waiting for room in
the download client".
