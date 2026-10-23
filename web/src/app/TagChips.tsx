import { useMemo, useState } from "react";

import type { components } from "@/api/schema";
import { useTagTree } from "@/lib/resources";

type TagNode = components["schemas"]["TagNode"];

/** A list's tag chips as the URL carries them, "12,-7": ids a work must carry, negative ones it must not. */
export function parseChips(value: string): number[] {
  return value.split(",").map((field) => Number(field.trim())).filter((id) => Number.isInteger(id) && id !== 0);
}

/** Each tag with its depth, parents before children, by name: the taxonomy's tree, then the free labels. */
export function orderedTags(tags: readonly TagNode[], taxonomy: string): { tag: TagNode; depth: number }[] {
  const mine = tags.filter((tag) => tag.taxonomy === taxonomy || tag.taxonomy === "");
  const children = new Map<number, TagNode[]>();
  const roots: TagNode[] = [];
  for (const tag of mine) {
    if (tag.parent_id && mine.some((other) => other.id === tag.parent_id)) {
      children.set(tag.parent_id, [...(children.get(tag.parent_id) ?? []), tag]);
    } else roots.push(tag);
  }
  const out: { tag: TagNode; depth: number }[] = [];
  const walk = (nodes: TagNode[], depth: number) => {
    for (const tag of [...nodes].sort((a, b) => (a.taxonomy === b.taxonomy ? a.name.localeCompare(b.name) : a.taxonomy === "" ? 1 : -1))) {
      out.push({ tag, depth });
      walk(children.get(tag.id) ?? [], depth + 1);
    }
  };
  walk(roots, 0);
  return out;
}

/** Filter by tags: pick from the library's genre tree and the free labels; a chip includes what is below it, a click
 *  on a chip turns it into "not", the cross removes it. */
export function TagChips({ taxonomy, value, onChange }: { taxonomy: string; value: string; onChange: (value: string) => void }) {
  const tree = useTagTree();
  const [search, setSearch] = useState("");
  const [open, setOpen] = useState(false);
  const chips = parseChips(value);
  const byId = useMemo(() => new Map((tree.data ?? []).map((tag) => [tag.id, tag])), [tree.data]);
  const options = useMemo(() => orderedTags(tree.data ?? [], taxonomy), [tree.data, taxonomy]);
  const needle = search.trim().toLowerCase();
  const shown = options.filter(({ tag }) => (needle === "" || tag.name.toLowerCase().includes(needle)) && !chips.some((chip) => Math.abs(chip) === tag.id));
  const set = (next: number[]) => onChange(next.join(","));

  return (
    <span className="flex flex-wrap items-center gap-1">
      <span
        className="relative"
        onBlur={(event) => {
          if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") setOpen(false);
        }}
      >
        <button
          type="button"
          aria-expanded={open}
          aria-label="Filter by tags"
          onClick={() => setOpen(!open)}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm"
        >
          Tags{chips.length > 0 ? ` (${chips.length})` : ""}
        </button>
        {open && (
        <div className="absolute left-0 top-full z-20 mt-1 flex w-72 flex-col gap-1 rounded-md border border-border-subtle bg-surface p-2 shadow-lg">
          <input
            autoFocus
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="Find a tag…"
            aria-label="Find a tag"
            className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
          />
          <ul className="max-h-80 overflow-y-auto text-sm">
            {shown.map(({ tag, depth }) => (
              <li key={tag.id}>
                <button
                  type="button"
                  onClick={() => {
                    set([...chips, tag.id]);
                    setSearch("");
                    setOpen(false);
                  }}
                  style={{ paddingLeft: `${needle ? 0.5 : 0.5 + depth}rem` }}
                  className="flex w-full items-center gap-2 rounded px-2 py-0.5 text-left hover:bg-canvas"
                >
                  <span className="min-w-0 flex-1 truncate">{tag.name}</span>
                  {tag.taxonomy === "" && <span className="text-xs text-ink-muted">label</span>}
                  <span className="text-xs tabular-nums text-ink-muted">{tag.works}</span>
                </button>
              </li>
            ))}
            {shown.length === 0 && <li className="px-2 py-1 text-ink-muted">{tree.isLoading ? "Loading…" : "No tag matches."}</li>}
          </ul>
        </div>
        )}
      </span>
      {chips.map((chip) => {
        const tag = byId.get(Math.abs(chip));
        const without = chip < 0;
        return (
          <span
            key={chip}
            className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs ${without ? "border-danger text-danger" : "border-accent text-accent"}`}
          >
            <button
              type="button"
              title={without ? "Leaving these out; click to show only these" : "Showing only these; click to leave them out"}
              onClick={() => set(chips.map((other) => (other === chip ? -other : other)))}
              className={without ? "line-through" : ""}
            >
              {without ? "not " : ""}
              {tag?.name ?? `#${Math.abs(chip)}`}
            </button>
            <button type="button" aria-label={`Remove the tag filter ${tag?.name ?? ""}`} onClick={() => set(chips.filter((other) => other !== chip))}>
              ×
            </button>
          </span>
        );
      })}
    </span>
  );
}

/** Tags on one work: chips for the chosen names and a field that suggests the taxonomy's genres and the free labels;
 *  Enter or a comma takes what was typed, a new label when no tag has the name. */
export function TagPicker({ taxonomy, names, onChange, label }: { taxonomy: string; names: string[]; onChange: (names: string[]) => void; label: string }) {
  const tree = useTagTree();
  const [text, setText] = useState("");
  const needle = text.trim().toLowerCase();
  const chosen = new Set(names.map((name) => name.toLowerCase()));
  const suggestions = needle === ""
    ? []
    : orderedTags(tree.data ?? [], taxonomy).filter(({ tag }) => tag.name.toLowerCase().includes(needle) && !chosen.has(tag.name.toLowerCase())).slice(0, 8);
  const add = (name: string) => {
    const trimmed = name.trim();
    if (trimmed !== "" && !chosen.has(trimmed.toLowerCase())) onChange([...names, trimmed]);
    setText("");
  };

  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-ink-muted">{label}</span>
      <div className="flex flex-wrap items-center gap-1 rounded-md border border-border-subtle bg-canvas px-2 py-1">
        {names.map((name) => (
          <span key={name} className="inline-flex items-center gap-1 rounded-full border border-border-subtle px-2 py-px text-xs">
            {name}
            <button type="button" aria-label={`Remove ${name}`} onClick={() => onChange(names.filter((other) => other !== name))}>
              ×
            </button>
          </span>
        ))}
        <input
          value={text}
          aria-label={label}
          onChange={(event) => (event.target.value.endsWith(",") ? add(event.target.value.slice(0, -1)) : setText(event.target.value))}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add(text);
            }
          }}
          placeholder={names.length === 0 ? "Jazz, Science Fiction, a label of your own…" : ""}
          className="min-w-32 flex-1 bg-transparent py-0.5 text-sm outline-none"
        />
      </div>
      {suggestions.length > 0 && (
        <ul className="flex flex-wrap gap-1">
          {suggestions.map(({ tag }) => (
            <li key={tag.id}>
              <button type="button" onClick={() => add(tag.name)} className="rounded-full border border-accent px-2 py-px text-xs text-accent">
                + {tag.name}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
