# MediaNexus

A Go-native media management, discovery and acquisition platform for movies, TV and music —
the successor to Radarr, Sonarr and Lidarr in one engine.

**Status:** pre-implementation. Architecture is settled; Phase 0 has not started.

## What makes it different

- **Work → Edition → File identity** — editions, remasters and alternate cuts are modelled
  as first-class, never mistaken for duplicates of each other.
- **Desired-state reconciliation** — a planner continuously closes the gap between wanted and
  owned, rather than firing timer-driven searches.
- **Explainable decisions** — every score, rejection and deferral carries structured reasons.
  "Why wasn't this downloaded?" is a query, not an investigation.
- **Relationship-aware library** — crossover episodes and music collaborations can be one
  physical file at several library paths, without duplicate false positives.
- **Provenance-tracked acquisition** — the chain from search term to library file survives
  renaming, so identity never depends on the final filename.
- **A real migration path** — import an existing *Arr setup, run alongside it in observe or
  shadow mode, compare decisions, then cut over.

## Design

Go, embedded SQLite, single static binary with the web UI compiled in. No external database,
message broker or cache server. Designed for libraries of 500,000+ files.

## Versioning

`0.0.x` — the patch number increments on every commit. Major and minor are changed by hand.

## Licence

Not yet chosen. Until it is, no GPL-licensed code (including from Radarr, Sonarr, Lidarr or
Prowlarr) may be incorporated — see the parser provenance rules in the architecture document.
