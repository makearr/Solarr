import { Link, type LinkProps } from "@tanstack/react-router";

export interface Tab {
  key: string;
  label: string;
  link: LinkProps;
}

/** Tabs whose tab is a route: linkable, kept on reload and on back. A tab the viewer may not use is left out. */
export function Tabs({ label, tabs, active }: { label: string; tabs: readonly Tab[]; active: string }) {
  return (
    <nav aria-label={label} className="flex flex-wrap items-end gap-x-1 border-b border-border-subtle text-sm">
      {tabs.map((tab) => (
        <Link
          key={tab.key}
          {...tab.link}
          aria-current={tab.key === active ? "page" : undefined}
          className={`-mb-px rounded-t-md border-b-2 px-3 py-1.5 focus-visible:outline-2 focus-visible:outline-offset-2
                      focus-visible:outline-accent ${
                        tab.key === active ? "border-accent font-medium text-ink" : "border-transparent text-ink-muted hover:text-ink"
                      }`}
        >
          {tab.label}
        </Link>
      ))}
    </nav>
  );
}
