# Web interface

Vite, React, TanStack Query and Tailwind, served from `embed.FS` (ADR-19, ADR-31).

## Where the bundle lives

`make web` writes it to `internal/webui/dist`, not `web/dist`: an embed pattern cannot climb
out of its own package directory.

Nothing in it is committed, but the directory must exist or `go:embed` has no pattern to match
and a clean checkout fails to build before anyone has run `make web`. Hence a kept `.gitkeep`,
the `all:` embed prefix so a dotfile still matches, and `emptyOutDir: false` so the build does
not delete it. The result is that building the interface leaves the tree clean, which
`make image` requires.

## Routing order

The interface handler answers everything it is given, because it cannot tell a client-side
route from a typo. It is registered **after** the API subtree, so chi leaves that subtree with
the NotFound handler in force when `Route` was called and a mistyped `/api/v1` path still
answers problem+json.

Move the registration above `Route` and every wrong API path returns the application shell with
status 200. `TestTheWebUINeverSwallowsAnAPIPath` holds that line.

## Caching

Fingerprinted assets are immutable; `index.html` is `no-cache`. The document names the current
hashed bundle, so a cached copy points at assets that no longer exist and the page loads blank.

## Theme

Applied by an inline script in `index.html` before first paint. Deciding it in React shows a
light flash on every load for anyone who chose dark.

This is the one thing kept in browser storage, because it is needed before any request can be
made. View state stays server-side per architecture section 21.2.

## The list core

`useLibraryList()` owns fetching, cursor pagination, sort and filter state, the selection model
and virtualisation. `ListTable` and `ListGrid` decide only what a row looks like — architecture
section 21.2's "built once, rendered twice".

**Selection is a query, not a set of rows** (section 21.7). "Select all matching" has to cover
items the virtualised list has never fetched, and deselecting three of 231 must not materialise
the other 228, so it is a mode plus a small exception set. The count is null until the server
reports a total: answering with the number of loaded rows would show "12 selected" to someone
who asked for all 231.

Changing a filter clears the selection. Keeping it would act on rows the user can no longer
see, and for "all matching" on an entirely different set.

Pages are fetched when the sentinel row enters the window rather than from a scroll handler,
which at 500k rows fires far more often than it needs to. Invalidation is SSE-driven: events are
lossy and for notification only (ADR-9).

The URL carries sort and filters, with `replaceState` rather than `pushState` — changing a
filter is not a navigation, and making it one turns Back into "undo one filter" thirty times.

## Linting

`npm run build` runs eslint and vitest before compiling. The rules of hooks are invisible to
`tsc`: a hook called after an early return type-checks perfectly and fails at runtime with
"rendered fewer hooks than expected". That has already happened here once.
