import { useQuery } from "@tanstack/react-query";
import { ChevronDown, X } from "lucide-react";
import { useState } from "react";

import { api, ok } from "@/api/client";

import { PersonPicker } from "./PersonPicker";

/** Filter a library's list to the works one person is credited on: type-ahead over the people the library holds. */
export function PersonFilter({ categoryId, value, onChange }: { categoryId: number; value: string; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false);
  const id = Number(value);
  const chosen = useQuery({
    queryKey: ["person", id],
    enabled: Number.isFinite(id) && id > 0,
    queryFn: () => ok(api.GET("/people/{id}", { params: { path: { id } } })),
  });
  return (
    <span className="flex items-center gap-1">
      <span
        className="relative"
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
        }}
      >
        <button type="button" aria-expanded={open} aria-label="Filter by person" onClick={() => setOpen(!open)} className="rounded-md border border-border-subtle px-3 py-1.5 text-sm">
          Person
          <ChevronDown aria-hidden className="ml-1 inline size-3.5" />
        </button>
        {open && (
          <div className="absolute left-0 top-full z-20 mt-1 w-72 rounded-md border border-border-subtle bg-surface p-2 shadow-lg">
            <PersonPicker
              autoFocus
              categoryId={categoryId}
              onPick={(person) => {
                onChange(String(person.id));
                setOpen(false);
              }}
            />
          </div>
        )}
      </span>
      {value !== "" && (
        <span className="inline-flex items-center gap-1 rounded-full border border-accent px-2 py-px text-xs">
          {chosen.data?.name ?? "…"}
          <button type="button" aria-label="Remove the person filter" onClick={() => onChange("")} className="text-ink-muted hover:text-ink">
            <X aria-hidden className="size-3" />
          </button>
        </span>
      )}
    </span>
  );
}
