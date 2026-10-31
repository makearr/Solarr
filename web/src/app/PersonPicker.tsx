import { useQuery } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import { inputStyle } from "@/ui/form";

/** Type-ahead over people: the names that contain what is typed, scoped to one library when given. */
export function PersonPicker({ categoryId, exclude, autoFocus = false, label = "Find a person", onPick }: {
  categoryId?: number;
  /** A person the list leaves out (the one being merged away). */
  exclude?: number;
  autoFocus?: boolean;
  label?: string;
  onPick: (person: { id: number; name: string }) => void;
}) {
  const [text, setText] = useState("");
  const needle = text.trim();
  const found = useQuery({
    queryKey: ["person-picker", categoryId ?? 0, needle],
    enabled: needle.length > 0,
    queryFn: () => ok(api.GET("/people", { params: { query: { q: needle, limit: 10, category: categoryId } } })),
  });
  const people = (found.data?.items ?? []).filter((person) => person.id !== exclude);
  return (
    <div className="flex flex-col gap-1">
      <input autoFocus={autoFocus} value={text} onChange={(event) => setText(event.target.value)} placeholder={`${label}…`} aria-label={label} className={inputStyle} />
      {needle.length > 0 && (
        <ul className="max-h-60 overflow-y-auto rounded-md border border-border-subtle bg-surface text-sm">
          {people.map((person) => (
            <li key={person.id}>
              <button type="button" onClick={() => onPick({ id: person.id, name: person.name })} className="flex w-full items-baseline gap-2 px-2 py-1 text-left hover:bg-canvas">
                <span className="min-w-0 flex-1 truncate">{person.name}</span>
                {person.disambiguation && <span className="truncate text-xs text-ink-muted">{person.disambiguation}</span>}
              </button>
            </li>
          ))}
          {people.length === 0 && <li className="px-2 py-1 text-ink-muted">{found.isLoading ? "Searching…" : "Nobody by that name."}</li>}
        </ul>
      )}
    </div>
  );
}
