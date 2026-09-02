import { Link, useRouterState } from "@tanstack/react-router";

import { ThemeToggle } from "./ThemeToggle";

/** The sidebar lists categories, not media types (section 21.1). Until
 *  categories exist it lists the sections that do. */
const SECTIONS = [
  { to: "/", label: "Dashboard" },
  { to: "/library", label: "Library" },
  { to: "/activity", label: "Activity" },
  { to: "/settings", label: "Settings" },
] as const;

export function Sidebar() {
  const path = useRouterState({ select: (state) => state.location.pathname });

  return (
    <nav
      aria-label="Sections"
      className="flex w-48 shrink-0 flex-col border-r border-border-subtle bg-surface"
    >
      <div className="flex items-center justify-between px-4 py-3">
        <span className="text-sm font-semibold tracking-tight">MediaNexus</span>
        <ThemeToggle />
      </div>

      <ul className="flex flex-col gap-0.5 px-2">
        {SECTIONS.map((section) => {
          // Every other route is a prefix match, so a detail page keeps its
          // section highlighted; "/" would otherwise match everything.
          const active =
            section.to === "/" ? path === "/" : path.startsWith(section.to);
          return (
            <li key={section.to}>
              <Link
                to={section.to}
                className={`block rounded-md px-2 py-1.5 text-sm focus-visible:outline-2
                            focus-visible:outline-offset-2 focus-visible:outline-accent ${
                              active
                                ? "bg-accent text-accent-ink"
                                : "text-ink-muted hover:bg-canvas hover:text-ink"
                            }`}
              >
                {section.label}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
