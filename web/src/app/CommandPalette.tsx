import * as RadixDialog from "@radix-ui/react-dialog";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Search } from "lucide-react";
import { useState } from "react";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { setOverlay, useOverlay } from "@/lib/overlays";
import { useLibraryTree } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useTheme } from "@/lib/useTheme";
import { useTier } from "@/lib/useTier";

import { useDebouncedValue } from "./useDebouncedValue";
import { ACTIVITY, entryTab, librarySections, SETTINGS, visibleTabs } from "./sections";

interface Entry {
  readonly id: string;
  readonly group: "pages" | "actions" | "items" | "people";
  readonly label: string;
  readonly hint?: string;
  readonly run: () => void;
}

const GROUPS = ["actions", "pages", "items", "people"] as const;
const MINIMUM_QUERY_LENGTH = 2;

/** Whether every word the person typed appears in the entry's label or hint. */
export function matches(entry: Pick<Entry, "label" | "hint">, query: string): boolean {
  const haystack = `${entry.label} ${entry.hint ?? ""}`.toLowerCase();
  return query.toLowerCase().split(/\s+/).filter(Boolean).every((word) => haystack.includes(word));
}

/** Ctrl+K: one box for every page and setting by name, the actions worth a shortcut, and a search of the library and its people.
 *  What the person may not open is not listed. */
export function CommandPalette() {
  if (useOverlay() !== "palette") return null;
  return <Palette />;
}

function Palette() {
  const navigate = useNavigate();
  const tier = useTier();
  const administrator = useIsAdministrator();
  const libraries = useLibraryTree();
  const mediaTypes = useMediaTypes();
  const theme = useTheme();
  const [query, setQuery] = useState("");
  const [chosen, setChosen] = useState(0);
  const debounced = useDebouncedValue(query.trim(), 250);
  const searchable = debounced.length >= MINIMUM_QUERY_LENGTH;
  const viewer = { tier, administrator };
  const close = () => setOverlay(null);
  const go = (to: string, extra: { params?: Record<string, string>; search?: Record<string, string | number> } = {}) => () => {
    close();
    void navigate({ to: to as never, params: extra.params as never, search: extra.search as never });
  };

  const rescan = useMutation({
    mutationFn: () => ok(api.POST("/schedules/{key}/run", { params: { path: { key: "scan" } } })),
    meta: { success: t("palette.rescanStarted") },
  });
  const found = useQuery({
    queryKey: ["search", debounced],
    queryFn: () => ok(api.GET("/search", { params: { query: { q: debounced, limit: 8 } } })),
    enabled: searchable,
    staleTime: 30_000,
  });
  const people = useQuery({
    queryKey: ["search-artists", debounced],
    queryFn: async () => {
      const { data, error } = await api.GET("/people", { params: { query: { q: debounced, limit: 5 } } });
      if (error) throw error;
      return data.items;
    },
    enabled: searchable,
    staleTime: 30_000,
  });

  const entries: Entry[] = [
    { id: "a:add", group: "actions", label: t("palette.addToLibrary"), run: go("/add") },
    ...(administrator
      ? [{ id: "a:rescan", group: "actions" as const, label: t("palette.rescan"), run: () => { close(); rescan.mutate(); } }]
      : []),
    { id: "a:theme", group: "actions", label: t("palette.switchTheme"), run: () => { close(); theme.flip(); } },
    { id: "a:keys", group: "actions", label: t("palette.showShortcuts"), run: () => setOverlay("shortcuts") },
    { id: "p:dashboard", group: "pages", label: t("nav.dashboard"), run: go("/") },
    { id: "p:discover", group: "pages", label: t("discover.page"), run: go("/discover") },
    { id: "p:calendar", group: "pages", label: t("nav.calendar"), run: go("/calendar") },
    { id: "p:people", group: "pages", label: t("nav.people"), run: go("/people") },
    { id: "p:profile", group: "pages", label: t("nav.profile"), run: go("/settings/user") },
    { id: "p:prefs", group: "pages", label: t("nav.preferences"), run: go("/settings/webui") },
    ...visibleTabs(ACTIVITY, viewer).map((tab) => ({ id: `p:${tab.path}`, group: "pages" as const, label: `${ACTIVITY.label}: ${tab.label}`, run: go(tab.path) })),
    ...visibleTabs(SETTINGS, viewer).map((tab) => ({
      id: `p:${tab.path}`, group: "pages" as const, label: t("palette.settingsPage", { page: tab.label }), hint: tab.group, run: go(tab.path),
    })),
    ...(libraries.data ?? []).flatMap((library) => {
      const kinds = [library, ...(library.members ?? [])].map((entry) => ({
        categoryId: String(entry.id), label: mediaTypes.find((info) => info.key === entry.media_type)?.work.many ?? entry.media_type,
      }));
      const levels = [library, ...(library.members ?? [])].flatMap((entry) =>
        entry.levels.map((level) => ({
          id: `p:level:${entry.id}:${level.key}`, group: "pages" as const, label: t("palette.libraryPage", { library: library.name, page: level.label }),
          run: go("/library/$categoryId/$level", { params: { categoryId: String(entry.id), level: level.key } }),
        })),
      );
      const sections = librarySections(String(library.id), kinds).flatMap((section) => {
        const tab = entryTab(section, viewer);
        return tab
          ? [{ id: `p:${section.key}`, group: "pages" as const, label: t("palette.libraryPage", { library: library.name, page: section.label }), run: go(tab.path, { search: tab.search }) }]
          : [];
      });
      return [...levels, ...sections];
    }),
    ...(searchable
      ? [
          ...(found.data ?? []).map((result) => ({
            id: `i:${result.work.id}`, group: "items" as const, label: result.work.title, hint: [result.work.artist, result.work.year].filter(Boolean).join(" · ") || undefined,
            run: go("/works/$id", { params: { id: String(result.work.id) } }),
          })),
          ...(people.data ?? []).map((person) => ({
            id: `u:${person.id}`, group: "people" as const, label: person.name, run: go("/people/$id", { params: { id: String(person.id) } }),
          })),
        ]
      : []),
  ];
  const shown = GROUPS.flatMap((group) => entries.filter((entry) => entry.group === group && (entry.group === "items" || entry.group === "people" || matches(entry, query))));
  const at = Math.min(chosen, Math.max(shown.length - 1, 0));

  return (
    <RadixDialog.Root open onOpenChange={(open) => !open && close()}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <RadixDialog.Content
          aria-describedby={undefined}
          className="fixed top-[12vh] left-1/2 z-50 flex max-h-[70vh] w-[min(40rem,calc(100vw-2rem))] -translate-x-1/2 flex-col overflow-hidden
                     rounded-lg border border-border-subtle bg-canvas shadow-xl"
        >
          <RadixDialog.Title className="sr-only">{t("palette.trigger")}</RadixDialog.Title>
          <div className="flex items-center gap-2 border-b border-border-subtle px-3">
            <Search aria-hidden className="size-4 shrink-0 text-ink-muted" />
            <input
              autoFocus
              role="combobox"
              aria-expanded
              aria-controls="palette-results"
              aria-activedescendant={shown[at] ? `palette-${shown[at].id}` : undefined}
              value={query}
              placeholder={t("palette.placeholder")}
              onChange={(event) => { setQuery(event.target.value); setChosen(0); }}
              onKeyDown={(event) => {
                if (event.key === "ArrowDown") { event.preventDefault(); setChosen(Math.min(at + 1, shown.length - 1)); }
                else if (event.key === "ArrowUp") { event.preventDefault(); setChosen(Math.max(at - 1, 0)); }
                else if (event.key === "Enter") { event.preventDefault(); shown[at]?.run(); }
              }}
              className="w-full bg-transparent py-3 text-sm outline-none placeholder:text-ink-muted"
            />
          </div>
          <ul id="palette-results" role="listbox" className="min-h-0 overflow-y-auto p-1 text-sm">
            {shown.length === 0 && <li className="px-3 py-6 text-center text-ink-muted">{(found.isFetching || people.isFetching) ? t("palette.searching") : t("palette.empty")}</li>}
            {shown.map((entry, index) => (
              <li key={entry.id}>
                {(index === 0 || shown[index - 1]!.group !== entry.group) && (
                  <div className="px-3 pt-2 pb-1 text-[0.6875rem] uppercase tracking-wide text-ink-muted">{t(`palette.${entry.group}`)}</div>
                )}
                <button
                  type="button"
                  role="option"
                  id={`palette-${entry.id}`}
                  aria-selected={index === at}
                  onMouseMove={() => setChosen(index)}
                  onClick={entry.run}
                  className={`flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left ${index === at ? "bg-accent text-accent-ink" : "hover:bg-surface"}`}
                >
                  <span className="min-w-0 flex-1 truncate">{entry.label}</span>
                  {entry.hint && <span className={`shrink-0 text-xs ${index === at ? "text-accent-ink/80" : "text-ink-muted"}`}>{entry.hint}</span>}
                </button>
              </li>
            ))}
          </ul>
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}
