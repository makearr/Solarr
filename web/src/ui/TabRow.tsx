import { Link } from "@tanstack/react-router";

/** One tab of a TabRow: where it goes, whether it is the current one, and an optional count. */
export interface TabRowItem {
  readonly key: string;
  readonly label: string;
  readonly to: string;
  readonly search?: Readonly<Record<string, string | number>>;
  readonly active: boolean;
  readonly badge?: number;
}

/** The one tab row every section and library page uses (a section's pages side by side above the page). */
export function TabRow({ items, label, lead, sticky = false }: {
  items: readonly TabRowItem[];
  label: string;
  /** A small name before the first tab, such as the library the row belongs to. */
  lead?: string;
  sticky?: boolean;
}) {
  return (
    <nav
      aria-label={label}
      className={`${sticky ? "sticky top-0 z-10" : ""} flex flex-wrap items-end gap-x-1 border-b border-border-subtle bg-canvas px-4 pt-2 text-sm`}
    >
      {lead && <span className="mr-2 pb-1.5 text-xs text-ink-muted">{lead}</span>}
      {items.map((item) => (
        <Link
          key={item.key}
          to={item.to}
          search={item.search}
          aria-current={item.active ? "page" : undefined}
          className={`-mb-px flex items-center gap-1.5 rounded-t-md border-b-2 px-3 py-1.5 focus-visible:outline-2 focus-visible:outline-offset-2
                      focus-visible:outline-accent ${
                        item.active
                          ? "border-accent font-medium text-ink"
                          : "border-transparent text-ink-muted hover:text-ink"
                      }`}
        >
          {item.label}
          {item.badge != null && item.badge > 0 && (
            <span className="rounded-full bg-border-subtle px-1.5 text-[0.6875rem] tabular-nums text-ink-muted">
              {item.badge.toLocaleString()}
            </span>
          )}
        </Link>
      ))}
    </nav>
  );
}
