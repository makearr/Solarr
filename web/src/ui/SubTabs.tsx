import { useNavigate, useSearch } from "@tanstack/react-router";

export interface SubTab {
  key: string;
  label: string;
  /** Hidden tabs are absent, never disabled. */
  hidden?: boolean;
  content: React.ReactNode;
}

/** A long settings page split into sub pages; the one shown lives in the address (`?page=`) so it survives a reload. */
export function SubTabs({ tabs }: { tabs: readonly SubTab[] }) {
  const navigate = useNavigate();
  const { page } = useSearch({ strict: false }) as { page?: string };
  const shown = tabs.filter((tab) => !tab.hidden);
  const active = shown.find((tab) => tab.key === page) ?? shown[0];
  if (!active) return null;
  return (
    <div className="flex flex-col gap-4">
      {shown.length > 1 && (
        <nav aria-label="Pages" className="flex flex-wrap gap-x-1 border-b border-border-subtle text-sm">
          {shown.map((tab) => (
            <button
              key={tab.key}
              type="button"
              aria-current={tab === active ? "page" : undefined}
              onClick={() => void navigate({ to: ".", replace: true, search: (previous: Record<string, unknown>) => ({ ...previous, page: tab.key }) })}
              className={`-mb-px rounded-t-md border-b-2 px-3 py-1.5 focus-visible:outline-2 focus-visible:outline-accent ${
                tab === active ? "border-accent font-medium text-ink" : "border-transparent text-ink-muted hover:text-ink"
              }`}
            >
              {tab.label}
            </button>
          ))}
        </nav>
      )}
      {active.content}
    </div>
  );
}
