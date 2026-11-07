# Solarr documentation

The project wiki. Anything needing more than a brief comment belongs here, in an appropriately
named file, linked from the code by path.

**Levels.** Every page opens with its level: 🟢 **Beginner** (run a library), 🟡 **Advanced** (decide where files go,
how they are named, how hard Solarr searches), 🔴 **Expert** (internals, and contributing). They match the
interface levels *normal / advanced / expert* in Settings → User.

**New? Read [getting-started.md](getting-started.md)**, then follow the [user guide](user-guide.md).

## 🟢 Beginner: using Solarr

| Page | Covers |
|---|---|
| [getting-started.md](getting-started.md) | Install, setup wizard, connect an indexer and client, first download: 15 minutes |
| [user-guide.md](user-guide.md) | The map of the guide, the interface levels, the colours |
| [guide/music.md](guide/music.md) | Add, edit and follow music; duplicates |
| [guide/tv.md](guide/tv.md) | TV and anime libraries, series, unidentified files, quality |
| [guide/automation.md](guide/automation.md) | What Solarr does by itself, how to watch it, how to tune it |
| [guide/people.md](guide/people.md) | People, labels, networks and following |
| [guide/favorites.md](guide/favorites.md) | Hearts, apart from follows, and the Favorites filter |
| [screenshots/](screenshots/) | The images the top-level README shows, from a demo instance with placeholder files |

## 🟡 Advanced: setting it up your way

| Page | Covers |
|---|---|
| [guide/connections.md](guide/connections.md) | Indexers, SABnzbd, qBittorrent, the category rule |
| [guide/libraries.md](guide/libraries.md) | Which library, moving works, several kinds in one, collections, saved filters |
| [guide/importing.md](guide/importing.md) | Import a folder, archives, held reviews |
| [guide/storage.md](guide/storage.md) | Root folders, offline roots, recycle bin, cache |
| [container-runtime.md](container-runtime.md) | PUID/PGID, TZ, ownership, capabilities |
| [environment-variables.md](environment-variables.md) | Every variable: defaults, precedence, API keys, root folders, `_FILE` secrets |
| [startup-checks.md](startup-checks.md) | How directories are checked at startup, and what deliberately is not |
| [configuration-export.md](configuration-export.md) | Export and import of the settings |
| [arr-import.md](arr-import.md) | Importing from the *Arrs by choice, Whisparr, and sending monitoring back |
| [import-lists.md](import-lists.md) | A library's import lists: sources, preview, sync |
| [import-rules.md](import-rules.md) | What an import accepts |
| [library-quality.md](library-quality.md) | Ordered quality profiles, cutoff and target, and what "fits" means |
| [naming.md](naming.md) | Slots, tokens, character replacement, apply to library |
| [file-management.md](file-management.md) | Sonarr-style folders, recycling bin, file dates and permissions |
| [downloads.md](downloads.md) | Downloads at once, held releases, who asked and the order they are sent in |
| [library-suggestions.md](library-suggestions.md) | Which library a work belongs in |
| [mixed-libraries.md](mixed-libraries.md) | A library of several media types |
| [rating-limit.md](rating-limit.md) | A library's maximum rating |
| [tags.md](tags.md) | Tags and genre trees |
| [metadata-providers.md](metadata-providers.md) | What each provider carries and which library it serves |
| [outbound-requests.md](outbound-requests.md) | The user, fresh and background request lanes and their shares |
| [books.md](books.md) | Books and audiobooks |
| [adult.md](adult.md) | The adult type |
| [media-player.md](media-player.md) | The preview player: direct and converted playback, GPU encoding |

## 🔴 Expert: how it works, and contributing

| Page | Covers |
|---|---|
| [music-identification.md](music-identification.md) | How a music file is identified: the passes, album matching, what was measured |
| [tv-internals.md](tv-internals.md) | The TV parser, providers, series storage and naming |
| [tv-providers-benchmark.md](tv-providers-benchmark.md) | Provider speed and richness, and the default order it set |
| [media-tools.md](media-tools.md) | ffprobe, fpcalc and the tagging helper: what Alpine has, what it costs |
| [database-layout.md](database-layout.md) | Which database a table belongs in, and why |
| [memory-copies.md](memory-copies.md) | Running the cache or the databases from RAM, and the guard when memory runs out |
| [users-and-access.md](users-and-access.md) | Roles, the per-library grants (request, add, manage) and hiding tagged items from an account |
| [benchmarks.md](benchmarks.md) | The 500k corpus, the performance targets as tests |
| [duplicate-videos.md](duplicate-videos.md) | Perceptual hashing of videos, the finder's levels, why not Stash's method, merging |
| [codebase-map.md](codebase-map.md) | The package tree, what depends on what, where to start reading |
| [coding-standards.md](coding-standards.md) | Style, structure, comments, testing |
| [sql-and-sqlc.md](sql-and-sqlc.md) | Writing queries, and the sqlc/SQLite constraints that bite silently |
| [api-generation.md](api-generation.md) | Spec-first generation and what it catches |
| [web-interface.md](web-interface.md) | Bundle, routing order, theme, the navigation tree, the list core |
| [versioning.md](versioning.md) | `YYYY.MM.R.BBBB`, milestones, releases |
| [licence-compliance.md](licence-compliance.md) | AGPL-3.0 and the §13 network clause |

## What goes here

- Algorithms that need explaining beyond a line or two
- Format specifications: release-name grammars, tag mappings, DAT files
- Provider quirks and workarounds
- Design rationale that belongs with the code
- Runbooks and operational procedures

## What does not

- Restating what the code plainly does
- Architecture decisions: those are ADRs
- Anything that will silently rot. A page nobody updates is worse than no page

A new page opens with its level line (`> **Level: 🟡 Advanced.** one sentence`) and is added to the table of
its level above.
