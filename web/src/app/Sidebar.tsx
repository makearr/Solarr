import { Link, useRouterState, useSearch } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Activity, LayoutDashboard, Library, Settings, type LucideIcon } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { ThemeToggle } from "./ThemeToggle";

type Category = components["schemas"]["LibraryCategory"];

/** Fixed sections: not categories, so not driven by /categories (section 21.1). */
const SECTIONS: readonly { to: string; label: string; icon: LucideIcon }[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/library", label: "Library", icon: Library },
  { to: "/activity", label: "Activity", icon: Activity },
  { to: "/settings", label: "Settings", icon: Settings },
];

export function Sidebar() {
  const path = useRouterState({ select: (state) => state.location.pathname });
  const search = useSearch({ strict: false }) as Record<string, unknown>;
  const activeCategoryId = typeof search.category_id === "string" ? search.category_id : null;

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  return (
    <nav
      aria-label="Sections"
      className="flex w-48 shrink-0 flex-col border-r border-border-subtle bg-surface"
    >
      <div className="flex items-center justify-between gap-2 px-4 py-3">
        <span className="text-sm font-semibold tracking-tight">MediaNexus</span>
        <ThemeToggle />
      </div>

      <ul className="flex flex-col gap-0.5 px-2">
        {SECTIONS.map(({ to, label, icon: Icon }) => {
          // Every other route is a prefix match, so a detail page keeps its
          // section highlighted; "/" would otherwise match everything.
          const active =
            to === "/" ? path === "/" : path.startsWith(to) && activeCategoryId === null;
          return (
            <li key={to}>
              <Link
                to={to}
                className={sectionLinkClass(active)}
              >
                <Icon aria-hidden className="size-4 shrink-0" />
                {label}
              </Link>
            </li>
          );
        })}
      </ul>

      {categories.data && categories.data.length > 0 && (
        <>
          <div className="px-4 pt-4 pb-1 text-xs font-medium uppercase tracking-wide
                           text-ink-muted">
            Categories
          </div>
          <ul className="flex flex-col gap-0.5 px-2">
            {categories.data.map((category) => (
              <CategoryLink
                key={category.id}
                category={category}
                active={path.startsWith("/library") && activeCategoryId === String(category.id)}
              />
            ))}
          </ul>
        </>
      )}
    </nav>
  );
}

function CategoryLink({ category, active }: { category: Category; active: boolean }) {
  return (
    <li>
      <Link
        to="/library"
        search={{ category_id: String(category.id) }}
        className={sectionLinkClass(active)}
      >
        <Library aria-hidden className="size-4 shrink-0" />
        <span className="truncate">{category.name}</span>
        {/* From the counter table (section 21.12), never a live COUNT; 0 until
            the reconciliation sweep has run at least once. */}
        {category.works != null && (
          <span className="ml-auto shrink-0 text-xs text-ink-muted">{category.works}</span>
        )}
      </Link>
    </li>
  );
}

function sectionLinkClass(active: boolean) {
  return `flex items-center gap-2 rounded-md px-2 py-1.5 text-sm
          focus-visible:outline-2 focus-visible:outline-offset-2
          focus-visible:outline-accent ${
            active
              ? "bg-accent text-accent-ink"
              : "text-ink-muted hover:bg-canvas hover:text-ink"
          }`;
}
