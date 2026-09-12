# Licence compliance

MediaNexus is **AGPL-3.0-or-later**. Most of that licence is satisfied by shipping `LICENSE`
and keeping `ATTRIBUTION.md` current. One clause is a **functional requirement** and is easy
to overlook.

## AGPL §13 — the network clause

> If you modify the Program, your modified version must prominently offer all users
> interacting with it remotely… an opportunity to receive the Corresponding Source.

MediaNexus is a web application, so anyone using it interacts remotely. The obligation is not
discharged by a file in the repository — **the running program must offer its source.**

### What this requires in the product

An **About** view, reachable from the main navigation, showing:

- Product name and version (from `VERSION`, compiled in at build time)
- The exact build: commit SHA, build date
- Licence: AGPL-3.0-or-later, with the full text viewable in-app
- **A link to the Corresponding Source for the running version**, not merely to the project
  homepage. For an unmodified build this is the upstream repository at that commit; a
  modified build must point at the modified source
- Third-party attributions from `ATTRIBUTION.md`

Also exposed at `GET /api/v1/about`, so the information is available without the UI.

### Implementation notes

- Version, commit and build date are injected via `-ldflags -X`. A build with an unknown
  commit must say so rather than showing a plausible-looking wrong value.
- The source URL is a build-time variable with the upstream repository as its default. A fork
  overrides it. **It must never silently point at upstream when the build is modified** — that
  is precisely the misrepresentation §13 exists to prevent.
- The About view is reachable **without authentication**, since "all users interacting with it
  remotely" is not limited to those who can log in.

### Checklist

- [x] `LICENSE` at repository root
- [x] `ATTRIBUTION.md` current
- [x] About view in the UI, unauthenticated (`/about`, checked before any auth gate in `App.tsx`)
- [x] `GET /api/v1/about` returning version, commit, licence, source URL (unauthenticated)
- [x] Source URL overridable at build time (`-X main.sourceURL`), defaulting to upstream
- [x] Full licence text viewable in-app (`GET /about/license`, embedded from `LICENSE` via
      `internal/api/apiserver/legal`, `make license-check` guards against drift)

### Known gap, not addressed here

`sourceURL` defaults to `https://github.com/makearr/MediaNexus`, which is **private**
(`CLAUDE.md` §8). For an unmodified build, the About view's "Corresponding Source" link is
not actually reachable by a third party today -- the obligation only bites once someone other
than the operator interacts with a deployed instance, which is not yet the case here. Revisit
before the repository or any built image is shared or exposed beyond the user's own use.
