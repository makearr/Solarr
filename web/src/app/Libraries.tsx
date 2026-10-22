import { Link } from "@tanstack/react-router";

import { mediaTypeIcon, mediaTypeName } from "@/lib/mediaTypeIcon";
import { useLibraries } from "@/lib/resources";
import type { components } from "@/api/schema";

import { AddLibraryForm } from "./NewLibrary";
import { defaultLevelKey } from "./Sidebar";

type LibraryEntry = components["schemas"]["Library"];

/** The Library section's own landing page (asked for 2026-09-13): the header
 *  in the sidebar is a link now, and this is where it lands -- every library
 *  as a card, and the only place a new one can be created from the interface.
 *  A library is a category (ADR-54), so creating one is POST /categories. */
export function Libraries() {
  const libraries = useLibraries();

  return (
    <div className="flex h-full flex-col gap-4 overflow-auto p-6">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Libraries</h1>
        <p className="text-sm text-ink-muted">Every collection Solarr manages.</p>
      </div>

      {libraries.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {libraries.error != null && <p className="text-sm text-danger">The libraries could not be read.</p>}

      {libraries.data && libraries.data.length === 0 && (
        <p className="text-sm text-ink-muted">No libraries yet. Add one below to get started.</p>
      )}

      {libraries.data && libraries.data.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {libraries.data.map((library) => (
            <LibraryCard key={library.id} library={library} />
          ))}
        </ul>
      )}

      <AddLibraryForm />
    </div>
  );
}

function LibraryCard({ library }: { library: LibraryEntry }) {
  const Icon = mediaTypeIcon(library.media_type);
  const level = defaultLevelKey(library);
  const content = (
    <>
      <span className="flex items-center gap-2">
        <Icon aria-hidden className="size-5 shrink-0 text-accent" />
        <span className="truncate font-medium">{library.name}</span>
        <span className="ml-auto shrink-0 text-xs uppercase tracking-wide text-ink-muted">{mediaTypeName(library.media_type)}</span>
      </span>
      <span className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-muted">
        {library.levels.map((entry) => (
          <span key={entry.key}>
            {entry.label}
            {entry.count != null && <span className="ml-1 text-ink">{entry.count}</span>}
          </span>
        ))}
      </span>
    </>
  );

  return (
    <li>
      {level ? (
        <Link
          to="/library/$categoryId/$level"
          params={{ categoryId: String(library.id), level }}
          className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4
                     hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          {content}
        </Link>
      ) : (
        <div className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">{content}</div>
      )}
    </li>
  );
}

