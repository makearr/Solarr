# What happens on its own, and how to follow it

> **Level: 🟢 Beginner** to watch what Solarr does; 🟡 **Advanced** to tune how often and how much.

## Follow what is going on (🟢)

| Where | What |
|---|---|
| **Activity → Queue** | What is downloading, and what is held waiting for room |
| **Activity → History** | What was grabbed, imported or failed, and why |
| **Activity → Notifications** (and the bell in the header) | What needs you: the warnings the dashboard shows, and the last week's failed downloads and imports |
| **Library → Wanted** | **Missing**, and under **Cutoff Unmet** the complete albums whose files are below the library's quality. **Search all** looks for a strictly better release of each |
| **Library → Statistics** | See [music.md](music.md#statistics-) and [tv.md](tv.md) |
| **Calendar** | Month, week, a seven-day forecast, one day, an agenda and a year view of the releases of artists you follow and albums you have asked for. Green has it, blue is wanted and not out yet, red wanted and out |

### Every work has one page

An album, a series: the same action row (Monitor, Search, Refresh, Edit, Remove) and tabs:

- **Overview**: a series' seasons as Sonarr shows them, an album's tracklist as Lidarr does.
- **Files**: what is on disk and what the probe measured.
- **History**.
- **Search**: when the work was last searched and when it will be searched again, or **why it will not**: a
  download on its way, no indexer, searching switched off, no space, or waiting for its release date.
  **Interactive search** lists every release found, best first, and names any indexer that failed or did not
  answer in time.

**Remove** can also delete the files (to the recycle bin) and keep the work from being added back by a follow or
an import list.

## The three scheduled tasks (🟢)

Settings → Scheduler. You do not need to press anything.

| Task | Runs | Does |
|---|---|---|
| **RSS sync** | every 15 minutes | Reads each indexer's newest releases and grabs one a wanted album accepts |
| **Search for what is missing** | every 15 minutes (a start; the hourly limit spreads the work) | Searches the most urgent wanted items |
| **Check downloads** | every minute | Follows each download to imported and cleaned up. A failed download is blocklisted and the album searched again |

Your own grabs always go to the download client. Automatic ones wait ("held") when its queue is full, and stop
when the library's root folders have no space above their reserve.

## Tuning the search (🟡)

- **RSS freshness window** (Settings → Libraries → Priority; default 48 hours): an album an RSS feed first
  showed a release for counts as newly arrived for this long. The **Freshness window** (default 7 days) does the
  same for the release date.
- **Automatic searches per hour** (Settings → Libraries → Automatic searching; default 30, 0 turns it off).
  **Search every** (default 15 minutes) sets how often one starts.
- Each run's searches are split **40% new releases, 40% older gaps, 20% upgrades** (items below the library's
  cutoff). A share one group leaves unused goes to the others.
- Each library has a **share** of the searches (default 1; 2 gets twice as many, 0 none). What one library
  cannot use goes to the others.
- **Automatic search order** (same section): oldest or newest searched, oldest or newest release, or a random
  pick among the oldest searched.
- **Backoff:** an album just searched waits 6 hours, then 12, 24, doubling to a week. After a failed download it
  is tried again within a day.
- **Upgrades:** an upgrade waits until the file it would replace is a day old, and is left alone once it has
  had three imports in a week.

Why queues hold and in what order: [downloads.md](../downloads.md).

## Tasks that refresh things (🟡)

| Task | Does |
|---|---|
| **Refresh works** | Re-reads running series twice a day so new episodes and seasons arrive by themselves, and fills in albums' tracklists |
| **Refresh people**, **Fetch pictures** | The same for people, and for covers and posters; one task each for every kind of library |
| **Compare discographies** | Weekly; feeds Discover |
| **Identify waiting files** (05:30) | Asks again about files a scan could not place in music, movies, books, audiobooks and adult libraries: the folders holding them are rescanned through the library's own media type, so a provider outage or an update that fixes a title no longer leaves them in Review for good |
| **Identify waiting TV files** | The same for TV; see [tv.md](tv.md) |
| **Add the new works of monitored franchises** | Every 12 hours; see [libraries.md](libraries.md) |

Any task can be run on demand from the Scheduler.
