# Solarr documentation

The project wiki. Anything needing more than a brief comment belongs here, in an appropriately
named file, linked from the code by path.

| Page | Covers |
|---|---|
| [user-guide.md](user-guide.md) | **For people running Solarr**: install, first run, connections, adding music, what happens on its own, import, the colours |
| [screenshots/](screenshots/) | The images the top-level README shows, taken from a demo instance whose files are placeholders |
| [codebase-map.md](codebase-map.md) | The package tree, what depends on what, and where to start reading |
| [coding-standards.md](coding-standards.md) | Style, structure, comments, testing, AI-maintainability |
| [sql-and-sqlc.md](sql-and-sqlc.md) | Writing queries, and the sqlc/SQLite constraints that bite silently |
| [database-layout.md](database-layout.md) | Which database a table belongs in, and why |
| [api-generation.md](api-generation.md) | Spec-first generation and what it catches |
| [web-interface.md](web-interface.md) | Bundle, routing order, theme, the navigation tree, the list core |
| [container-runtime.md](container-runtime.md) | PUID/PGID, TZ, ownership, capabilities |
| [startup-checks.md](startup-checks.md) | How directories are checked at startup, and what deliberately is not |
| [configuration-export.md](configuration-export.md) | F9 export and import |
| [library-quality.md](library-quality.md) | A library's ordered quality profiles, cutoff and target, and what "fits" means |
| [library-suggestions.md](library-suggestions.md) | Which library a work belongs in: facts, rules per library and kind, the check |
| [mixed-libraries.md](mixed-libraries.md) | A library of several media types: member categories, what they share, the All list |
| [rating-limit.md](rating-limit.md) | A library's maximum rating: how certifications become ages, and what a work above it is |
| [tags.md](tags.md) | Tags and genre trees: shipped trees, aliases, where a provider's genre lands, the tag filter |
| [naming.md](naming.md) | How a library names files: slots, tokens, character replacement, apply to library |
| [file-management.md](file-management.md) | A library's Sonarr-style folders, recycling bin, file dates and permissions |
| [downloads.md](downloads.md) | Downloads at once, held releases, who asked and the order they are sent in |
| [music-identification.md](music-identification.md) | How a music file is identified: the passes, album matching, what was measured |
| [tv-internals.md](tv-internals.md) | Stage 8 as built: the TV parser, providers, series storage and naming |
| [tv-providers-benchmark.md](tv-providers-benchmark.md) | Measured speed and richness of the TV metadata providers, and the default order it set |
| [arr-import.md](arr-import.md) | Importing from the *Arrs by choice (paths, items, monitoring only, indexers, download clients), Whisparr, and sending monitoring back |
| [adult.md](adult.md) | The adult type: scenes and films, performers and studios, the stash-box and Whisparr providers, identification by oshash and name |
| [books.md](books.md) | Books and audiobooks: two media types over one module, their providers, files, search and naming |
| [import-lists.md](import-lists.md) | A library's import lists: the sources, how an entry becomes a work, preview, the sync |
| [metadata-providers.md](metadata-providers.md) | What each metadata provider carries, which library it serves, what the upstream projects use, and how collections are found |
| [media-tools.md](media-tools.md) | ADR-30's ffprobe, fpcalc and tagging helper: what Alpine has, what it costs |
| [benchmarks.md](benchmarks.md) | The 500k corpus, the ADR-28 targets as tests, the ADR-5 driver switch |
| [versioning.md](versioning.md) | `YYYY.MM.R.BBBB`, what advances it, milestones, what a release (`make image`) does |
| [licence-compliance.md](licence-compliance.md) | AGPL-3.0 and the §13 network clause |

## What goes here

- Algorithms that need explaining beyond a line or two
- Format specifications — release-name grammars, tag mappings, DAT files
- Provider quirks and workarounds
- Design rationale that belongs with the code rather than in the architecture document
- Runbooks and operational procedures

## What does not

- Restating what the code plainly does
- Architecture decisions — those are ADRs
- Anything that will silently rot. A page nobody updates is worse than no page
