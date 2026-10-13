import { useEffect, useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { ArrowDown, ArrowUp, Plus, Search } from "lucide-react";

const control = "rounded-md border border-border-subtle bg-surface px-1.5 py-1 text-ink";

/** The head of every index: its name, quick choices beside it, and the controls on the right in one order:
 *  quick filters, search, Filter, Sort, columns or picture options, the view, Add new. */
export function Toolbar({ title, chips, children }: { title: ReactNode; chips?: ReactNode; children?: ReactNode }) {
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2 text-xs">
      <h1 className="mr-2 text-sm font-medium">{title}</h1>
      {chips}
      <div className="ml-auto flex flex-wrap items-center gap-2">{children}</div>
    </div>
  );
}

/** A labelled choice in the toolbar ("Show", "Sort"). */
export function ToolbarSelect({
  label,
  value,
  onChange,
  children,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  children: ReactNode;
}) {
  return (
    <label className="flex items-center gap-1 text-ink-muted">
      {label}
      <select value={value} onChange={(event) => onChange(event.target.value)} className={control}>
        {children}
      </select>
    </label>
  );
}

/** The index's own search box; what is typed is handed on once the typing pauses. */
export function ToolbarSearch({
  value,
  onChange,
  placeholder,
  label,
}: {
  value: string;
  onChange: (value: string) => void;
  placeholder: string;
  label: string;
}) {
  const [typed, setTyped] = useState(value);
  // A change from outside replaces what is typed; the trimmed echo of the typing itself must not.
  useEffect(() => setTyped((now) => (now.trim() === value ? now : value)), [value]);
  useEffect(() => {
    const timer = setTimeout(() => {
      if (typed.trim() !== value) onChange(typed.trim());
    }, 250);
    return () => clearTimeout(timer);
  }, [typed, value, onChange]);
  return (
    <label className="flex items-center gap-1.5 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-ink-muted">
      <Search aria-hidden className="size-3.5" />
      <input
        value={typed}
        onChange={(event) => setTyped(event.target.value)}
        placeholder={placeholder}
        aria-label={label}
        className="w-40 bg-transparent text-ink outline-none"
      />
    </label>
  );
}

/** What the index is ordered by, and a button that turns the order round. */
export function ToolbarSort({
  sort,
  descending,
  choices,
  onSort,
  onTurn,
}: {
  sort: string;
  descending: boolean;
  choices: readonly (readonly [string, string])[];
  onSort: (sort: string) => void;
  onTurn: () => void;
}) {
  const Arrow = descending ? ArrowDown : ArrowUp;
  return (
    <>
      <ToolbarSelect label="Sort" value={sort} onChange={onSort}>
        {choices.map(([key, label]) => (
          <option key={key} value={key}>
            {label}
          </option>
        ))}
      </ToolbarSelect>
      <button
        type="button"
        aria-label={descending ? "Descending: turn the order round" : "Ascending: turn the order round"}
        title={descending ? "Descending" : "Ascending"}
        onClick={onTurn}
        className="rounded-md border border-border-subtle p-1.5 text-ink-muted hover:text-ink"
      >
        <Arrow aria-hidden className="size-3.5" />
      </button>
    </>
  );
}

/** The way to add something to this library; always the last control. */
export function ToolbarAdd({ categoryId }: { categoryId: string }) {
  return (
    <Link
      to="/library/$categoryId/discover"
      params={{ categoryId }}
      className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
    >
      <Plus aria-hidden className="size-3.5" />
      Add new
    </Link>
  );
}
