import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

/** A library's own landing page -- the *Arr apps' own default view, asked
 *  for 2026-09-11: land on what the library actually holds, not a
 *  cross-library dashboard. Reuses /libraries (already fetched by the
 *  sidebar) rather than a dedicated endpoint that does not exist. */
export function Overview() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };

  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });

  const library = libraries.data?.find((entry) => String(entry.id) === categoryId);

  if (libraries.isLoading) {
    return <p className="p-6 text-sm text-ink-muted">Loading…</p>;
  }
  if (!library) {
    return <p className="p-6 text-sm text-ink-muted">This library could not be found.</p>;
  }

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-6 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">{library.name}</h1>
        <p className="text-xs text-ink-muted">{library.media_type}</p>
      </header>

      {library.levels.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2">
          {library.levels.map((level) => (
            <li key={level.key}>
              <Link
                to="/library/$categoryId/$level"
                params={{ categoryId: String(library.id), level: level.key }}
                className="flex items-center justify-between rounded-lg border border-border-subtle
                           bg-surface px-3 py-2.5 hover:border-accent focus-visible:outline-2
                           focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                <span className="text-sm font-medium">{level.label}</span>
                {level.count != null && <span className="text-xs text-ink-muted">{level.count}</span>}
              </Link>
            </li>
          ))}
        </ul>
      )}

      <section className="flex flex-col gap-2">
        <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Shortcuts</h2>
        <ul className="grid gap-3 sm:grid-cols-2">
          <ShortcutLink to="/library/$categoryId/discover" categoryId={library.id} label="Add new" />
          <ShortcutLink to="/library/$categoryId/wanted" categoryId={library.id} label="Wanted" />
          <ShortcutLink to="/library/$categoryId/import" categoryId={library.id} label="Import" />
          <ShortcutLink to="/library/$categoryId/activity" categoryId={library.id} label="Activity" />
        </ul>
      </section>
    </div>
  );
}

function ShortcutLink({
  to,
  categoryId,
  label,
}: {
  to: "/library/$categoryId/discover" | "/library/$categoryId/wanted" | "/library/$categoryId/import" | "/library/$categoryId/activity";
  categoryId: number;
  label: string;
}) {
  return (
    <li>
      <Link
        to={to}
        params={{ categoryId: String(categoryId) }}
        className="flex items-center rounded-lg border border-border-subtle bg-surface px-3 py-2.5
                   text-sm hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        {label}
      </Link>
    </li>
  );
}
