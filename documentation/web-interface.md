# Web interface

Vite, React, TanStack Query and Tailwind, served from `embed.FS` (ADR-19, ADR-31).

## Where the bundle lives

`make web` writes it to `internal/api/webui/dist`, not `web/dist`: an embed pattern cannot climb
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

Two settings (2026-09-30): the mode (System, Light, Dark) and the colour scheme. A scheme in
`SCHEMES` (`src/lib/theme.ts`) names its light and dark token set; one with a single set (Jellyfin,
Arr, Nord, Paper) keeps it whatever the mode, and the mode picker is disabled for it. Stored as
`solarr.mode` and `solarr.scheme`; a single `solarr.theme` from before the split is read once and
mapped to its scheme and base.

The top bar carries one switch that flips light and dark (`ThemeToggle`); the mode including System and the
colour scheme are in the user menu under Appearance. Both read and write one shared choice (`src/lib/useTheme.ts`),
so they always agree. A scheme with a single token set disables the switch and the mode.

A token set is a block of values in `src/index.css`, an entry in `THEMES`, and its place in
`SCHEMES` and in `index.html`'s pre-paint copy of it. The Lidarr, Sonarr and Radarr schemes
(2026-09-29) come light and dark; the light ones colour the sidebar and header differently from
the page. Those two elements carry `data-region="sidebar"` / `"header"`, and a scheme redefines
the same tokens inside that scope (plus `--color-header`, the header bar's ground, which is the
page's own everywhere else). No component names a scheme.

## Status bar

Left: the latest event (downloads, imports). Beside it, what is running now, from
`GET /system/activity`, polled every 3 s: the executor lists each job while it runs
(`jobs.Running`), and a handler reports through `job.Progress` (`Set`, `Add`, `SetTotal`). The
rate is measured over the last one to five minutes and the estimate is the rest at that rate;
without a total (a first scan, the cover sweep) the count is shown alone. Only job types with a
`Definition.Name` appear, so a one-second housekeeping job does not flicker in the bar; a
library's job is shown only to those who can see that library.

## The navigation tree (ADR-54)

**One sidebar entry per section, its pages as tabs** (2026-10-04; the sidebar listed every page before). After
its own levels every library has the same three entries: Wanted (Wanted, Activity, Statistics), Files
(Unidentified, Import, Duplicates, Packs) and Settings (Quality, Naming, File management, Import lists). Activity,
Storage, Connections and Settings are single entries too. `src/app/sections.ts` declares each section and its tabs
with the tier and role that may see them; `SectionTabs` draws the tabs above whatever page the address names, so a
page does not know it is a tab and every address stayed what it was. A tab the viewer may not open is absent, and a
section's entry returns to the tab last opened there.

The sidebar reads `GET /libraries`: each category the caller may see, with the entity levels
its media type declares (`NavigationLevels`, an ADR-23 extension point — core renders the
declaration and never learns what an album is) and each level's count from the `counter` table.

`/library/$categoryId/albums` is the existing `Library` screen (cover art, bulk actions,
configurable columns) unchanged in substance — only its category now comes from the route's own
path param rather than a `category_id` search param, which keeps the query cache key and "select
all matching" correctly scoped: both read `listQuery.filters`, so `category_id` is injected there
from the route rather than carried a second way.

`/library/$categoryId/$level` is `LibraryLevelList`, generic over `GET /libraries/{id}/{level}`'s
own generic row shape (`LibraryLevelItem`: id, title, subtitle, year). Only one media type exists
today, so there is no second real case yet to generalize a typed-per-level response from — a
level's own detail lives behind the existing typed detail endpoint a row links to
(`GET /works/{id}`, etc.), not in the list row itself. A level whose reader is not wired up yet
answers 503 with a real detail message, rendered as the list's own empty state, so "no data" and
"not built yet" are never confused for each other.

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

The URL carries sort and filters, and **the router owns it**. `useLibraryList` takes its view
state rather than holding it: `useRouteView` reads the router's search parameters and writes
them back with `replace`, because changing a filter is not a navigation and making it one turns
Back into "undo one filter" thirty times.

Reading `window.location` instead leaves two owners of one URL, and the list loses. It held the
query in `useState`, initialised from the search string, so the initialiser ran once at mount: a
navigation to the same route with different filters re-rendered the component and changed
nothing — which is exactly what a dashboard figure linking into a filtered view does
(section 21.12). `useLibraryList.test.tsx` holds that line.

A list's source object is written inline at the call site, so **the hook must not depend on the
identity of anything in it**. `topics` is joined into a string before it reaches the effect
dependencies; depending on the array closed and reopened the event stream on every render.

## Components

Radix primitives, added per component as one is needed, styled with the existing Tailwind. The
value is the behaviour that is tedious and easy to get wrong — focus traps, roving tabindex,
escape and dismissal — not the styling. shadcn/ui was considered and declined: it is Radix plus
a styling layer, copied in as owned source, and that is a second set of conventions to reconcile
and keep in step for a benefit this interface does not need.

## Testing

Two kinds. The pure logic — `query.ts`, `selection.ts`, the virtualisation window — is tested
without React. The hook's wiring needs a DOM, so vitest runs under jsdom with Testing Library,
and `useLibraryList.test.tsx` drives the hook inside a real memory router. That is what makes
the URL binding and the event stream assertable rather than reasoned about; both were wrong
until it existed, and both mutations are caught by it.

## Linting

`npm run build` runs eslint and vitest before compiling. The rules of hooks are invisible to
`tsc`: a hook called after an early return type-checks perfectly and fails at runtime with
"rendered fewer hooks than expected". That has already happened here once.
