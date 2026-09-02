import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

/** Settings sections in the order they matter to someone setting the thing up.
 *  `stage` names what has to exist before a section can do anything, so the
 *  scaffolding is honest about being scaffolding rather than looking broken. */
const SECTIONS = [
  { id: "storage", title: "Storage", blurb: "Library roots, placement and free-space limits." },
  { id: "categories", title: "Categories", blurb: "The policy boundary for naming, profiles and access.", stage: 3 },
  { id: "profiles", title: "Quality profiles", blurb: "Hard gates, tier order and scoring.", stage: 4 },
  { id: "indexers", title: "Indexers", blurb: "Newznab and Torznab sources, and their egress.", stage: 5 },
  { id: "downloaders", title: "Download clients", blurb: "Usenet, torrent and direct downloads.", stage: 5 },
  { id: "metadata", title: "Metadata", blurb: "Providers, field priority and refresh cadence.", stage: 3 },
  { id: "users", title: "Users and access", blurb: "Accounts, API keys and per-category permission.", stage: 12 },
  { id: "general", title: "General", blurb: "Interface, backups and maintenance." },
] as const;

export function Settings() {
  const roots = useQuery({
    queryKey: ["rootfolders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/rootfolders");
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">Settings</h1>
          <p className="text-xs text-ink-muted">
            Every setting is normal, advanced or expert (ADR-26). The tier selector arrives with
            the forms themselves.
          </p>
        </header>

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Storage</h2>
          {roots.data && roots.data.length > 0 ? (
            <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle
                           bg-surface text-sm">
              {roots.data.map((root) => (
                <li key={root.id} className="flex items-center gap-3 px-3 py-2">
                  <span className="font-medium">{root.label ?? "unlabelled"}</span>
                  <span className="truncate font-mono text-xs text-ink-muted">{root.path}</span>
                  <span className="ml-auto shrink-0 text-xs text-ink-muted">
                    {root.writable === false ? "read-only" : "writable"}
                    {" · "}
                    {root.online ? "online" : "offline"}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
              No library root is configured.
            </p>
          )}
        </section>

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Sections</h2>
          <ul className="grid gap-3 sm:grid-cols-2">
            {SECTIONS.map((section) => (
              <li
                key={section.id}
                className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface
                           px-3 py-2.5"
              >
                <span className="flex items-baseline gap-2">
                  <span className="text-sm font-medium">{section.title}</span>
                  {"stage" in section && (
                    <span className="ml-auto shrink-0 rounded-full border border-border-subtle
                                     px-1.5 py-px text-[0.6875rem] text-ink-muted">
                      stage {section.stage}
                    </span>
                  )}
                </span>
                <span className="text-xs text-ink-muted">{section.blurb}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}
