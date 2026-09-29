# Downloads: what is sent, and what waits

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
