import { useNavigate, useSearch } from "@tanstack/react-router";

import { useLibraries } from "@/lib/resources";
import { useLibraryLabel } from "@/lib/libraryLabel";

import { ImportLists } from "./ImportLists";

/** Import lists among the connections: pick the library, then its lists are the ones on its own Import lists page. */
export function ConnectionImportLists() {
  const libraries = useLibraries().data ?? [];
  const libraryLabel = useLibraryLabel();
  const navigate = useNavigate();
  const { library } = useSearch({ strict: false }) as { library?: string };
  const chosen = library ?? (libraries[0] ? String(libraries[0].id) : undefined);
  return (
    <div className="flex h-full flex-col">
      <label className="flex items-center gap-2 px-6 pt-4 text-sm">
        <span className="text-ink-muted">Library</span>
        <select
          value={chosen ?? ""}
          onChange={(event) => void navigate({ to: "/connections/import-lists", search: { library: event.target.value } })}
          className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
        >
          {libraries.map((entry) => (
            <option key={entry.id} value={String(entry.id)}>
              {libraryLabel(entry)}
            </option>
          ))}
        </select>
      </label>
      <div className="min-h-0 flex-1">{chosen && <ImportLists key={chosen} library={chosen} />}</div>
    </div>
  );
}
