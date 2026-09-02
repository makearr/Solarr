# MediaNexus documentation

The project wiki. Anything needing more than a brief comment belongs here, in an appropriately
named file, linked from the code by path.

| Page | Covers |
|---|---|
| [codebase-map.md](codebase-map.md) | Where things live, what depends on what, and where to start reading |
| [coding-standards.md](coding-standards.md) | Style, structure, comments, testing, AI-maintainability |
| [startup-checks.md](startup-checks.md) | How directories are checked at startup, and what is deliberately not checked |
| [sql-and-sqlc.md](sql-and-sqlc.md) | Writing queries, and the sqlc SQLite constraints that bite silently |
| [benchmarks.md](benchmarks.md) | The 500k corpus, the ADR-28 targets as tests, and the ADR-5 driver switch |

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
- [container-runtime.md](container-runtime.md) — PUID/PGID, TZ, ownership, capabilities
- [web-interface.md](web-interface.md) — bundle, routing order, theme, the list core
- [api-generation.md](api-generation.md) — spec-first generation and what it catches
- [configuration-export.md](configuration-export.md) — F9 export and import
- [database-layout.md](database-layout.md) — which database a table belongs in, and why
