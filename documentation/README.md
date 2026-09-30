# Solarr documentation

The project wiki. Anything needing more than a brief comment belongs here, in an appropriately
named file, linked from the code by path.

| Page | Covers |
|---|---|
| [user-guide.md](user-guide.md) | **For people running Solarr**: install, first run, connections, adding music, what happens on its own, import, the colours |
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
| [naming.md](naming.md) | How a library names files: slots, tokens, character replacement, apply to library |
| [file-management.md](file-management.md) | A library's Sonarr-style folders, recycling bin, file dates and permissions |
| [downloads.md](downloads.md) | Downloads at once, held releases, who asked and the order they are sent in |
| [music-identification.md](music-identification.md) | How a music file is identified: the passes, album matching, what was measured |
| [media-tools.md](media-tools.md) | ADR-30's ffprobe, fpcalc and tagging helper: what Alpine has, what it costs |
| [benchmarks.md](benchmarks.md) | The 500k corpus, the ADR-28 targets as tests, the ADR-5 driver switch |
| [versioning.md](versioning.md) | `year.month.release.build`, milestones, what `make image` does |
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
