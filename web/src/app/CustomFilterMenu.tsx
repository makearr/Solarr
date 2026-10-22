import * as Dialog from "@radix-ui/react-dialog";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Check, Filter, Plus, Trash2, X } from "lucide-react";
import { useState } from "react";

import { usePreference } from "@/lib/usePreference";
import { Button } from "@/ui/Button";
import { describe, OPERATORS, parseWhere, toWhere, type Condition, type FilterField, type SavedFilter } from "@/list/customFilter";

const control = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm text-ink";

interface Props {
  /** The fields a condition may name, in the order the builder offers them. */
  fields: readonly FilterField[];
  /** Where this list's saved filters are kept, per person: one key per list. */
  storageKey: string;
  /** The conditions in force, as the JSON the URL carries. */
  where: string;
  onChange: (where: string) => void;
}

const blankCondition = (fields: readonly FilterField[]): Condition => {
  const first = fields[0]!;
  return { field: first.key, op: OPERATORS[first.kind][0]![0], value: first.kind === "bool" ? "true" : first.kind === "enum" ? (first.choices?.[0]?.[0] ?? "") : "" };
};

/** Sonarr's Filter menu: all, the filters a person saved, and a builder for a new one. */
export function CustomFilterMenu({ fields, storageKey, where, onChange }: Props) {
  const [saved, setSaved] = usePreference<SavedFilter[]>(`filters.${storageKey}`, []);
  const [editing, setEditing] = useState<SavedFilter | "new" | null>(null);
  const active = parseWhere(where);
  const current = saved.find((filter) => toWhere(filter.conditions) === where);
  const label = where === "" ? "Filter" : current?.name || "Custom filter";

  const save = (filter: SavedFilter, apply: boolean) => {
    // A filter without a name is applied but never kept.
    if (filter.name !== "") {
      setSaved(saved.some((entry) => entry.id === filter.id) ? saved.map((entry) => (entry.id === filter.id ? filter : entry)) : [...saved, filter]);
    }
    if (apply) onChange(toWhere(filter.conditions));
  };

  return (
    <>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger
          className={`flex shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-1 text-xs ${where === "" ? "border-border-subtle text-ink-muted hover:text-ink" : "border-accent text-ink"}`}
        >
          <Filter aria-hidden className="size-3.5" />
          {label}
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content align="end" sideOffset={4} className="z-50 min-w-56 rounded-md border border-border-subtle bg-surface p-1 text-sm shadow-lg">
            <DropdownMenu.Item
              onSelect={() => onChange("")}
              className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
            >
              <span className="size-3.5">{where === "" && <Check aria-hidden className="size-3.5" />}</span>
              Everything
            </DropdownMenu.Item>
            {saved.map((filter) => (
              <DropdownMenu.Item
                key={filter.id}
                onSelect={() => onChange(toWhere(filter.conditions))}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
              >
                <span className="size-3.5">{current?.id === filter.id && <Check aria-hidden className="size-3.5" />}</span>
                <span className="min-w-0 flex-1 truncate" title={filter.conditions.map((condition) => describe(condition, fields)).join(" and ")}>
                  {filter.name}
                </span>
              </DropdownMenu.Item>
            ))}
            <DropdownMenu.Separator className="my-1 h-px bg-border-subtle" />
            <DropdownMenu.Item
              onSelect={() => setEditing("new")}
              className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
            >
              <Plus aria-hidden className="size-3.5" />
              Custom filter…
            </DropdownMenu.Item>
            {current && (
              <DropdownMenu.Item
                onSelect={() => setEditing(current)}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
              >
                <span className="size-3.5" />
                Edit “{current.name}”
              </DropdownMenu.Item>
            )}
            {where !== "" && !current && (
              <DropdownMenu.Item
                onSelect={() => setEditing({ id: "", name: "", conditions: active })}
                className="flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas"
              >
                <span className="size-3.5" />
                Save this filter…
              </DropdownMenu.Item>
            )}
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
      {editing !== null && (
        <FilterBuilder
          fields={fields}
          initial={editing === "new" ? { id: "", name: "", conditions: active.length > 0 ? active : [blankCondition(fields)] } : editing}
          onClose={() => setEditing(null)}
          onSave={(filter, apply) => save(filter, apply)}
          onDelete={(id) => {
            setSaved(saved.filter((entry) => entry.id !== id));
            if (current?.id === id) onChange("");
          }}
        />
      )}
    </>
  );
}

function FilterBuilder({
  fields,
  initial,
  onClose,
  onSave,
  onDelete,
}: {
  fields: readonly FilterField[];
  initial: SavedFilter;
  onClose: () => void;
  onSave: (filter: SavedFilter, apply: boolean) => void;
  onDelete: (id: string) => void;
}) {
  const [name, setName] = useState(initial.name);
  const [conditions, setConditions] = useState<Condition[]>(initial.conditions.length > 0 ? initial.conditions : [blankCondition(fields)]);
  const set = (index: number, change: Partial<Condition>) => setConditions(conditions.map((condition, i) => (i === index ? { ...condition, ...change } : condition)));
  const complete = conditions.length > 0 && conditions.every((condition) => condition.value !== "");

  const changeField = (index: number, key: string) => {
    const field = fields.find((entry) => entry.key === key);
    if (!field) return;
    set(index, { field: key, op: OPERATORS[field.kind][0]![0], value: field.kind === "bool" ? "true" : field.kind === "enum" ? (field.choices?.[0]?.[0] ?? "") : "" });
  };
  const finish = (apply: boolean) => {
    const id = initial.id || `f${Date.now().toString(36)}`;
    // A filter without a name is applied but not kept.
    if (name.trim() !== "") onSave({ id, name: name.trim(), conditions }, apply);
    else if (apply) onSave({ id: "", name: "", conditions }, true);
    onClose();
  };

  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[min(40rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-4 shadow-xl">
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">{initial.id ? "Edit filter" : "Custom filter"}</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-xs text-ink-muted">Everything in the list must match every condition. Give it a name to keep it in the Filter menu.</Dialog.Description>
          <label className="flex items-center gap-2 text-xs text-ink-muted">
            Name
            <input value={name} onChange={(event) => setName(event.target.value)} placeholder="Optional" className={`${control} w-64`} />
          </label>
          <ul className="flex flex-col gap-2 overflow-y-auto">
            {conditions.map((condition, index) => {
              const field = fields.find((entry) => entry.key === condition.field) ?? fields[0]!;
              return (
                <li key={index} className="flex flex-wrap items-center gap-2">
                  <select value={condition.field} onChange={(event) => changeField(index, event.target.value)} aria-label="Field" className={control}>
                    {fields.map((entry) => (
                      <option key={entry.key} value={entry.key}>
                        {entry.label}
                      </option>
                    ))}
                  </select>
                  <select value={condition.op} onChange={(event) => set(index, { op: event.target.value })} aria-label="Condition" className={control}>
                    {OPERATORS[field.kind].map(([key, text]) => (
                      <option key={key} value={key}>
                        {text}
                      </option>
                    ))}
                  </select>
                  {field.kind === "bool" ? (
                    <select value={condition.value} onChange={(event) => set(index, { value: event.target.value })} aria-label="Value" className={control}>
                      <option value="true">yes</option>
                      <option value="false">no</option>
                    </select>
                  ) : field.kind === "enum" ? (
                    <select value={condition.value} onChange={(event) => set(index, { value: event.target.value })} aria-label="Value" className={control}>
                      {(field.choices ?? []).map(([key, text]) => (
                        <option key={key} value={key}>
                          {text}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      type={field.kind === "number" ? "number" : "text"}
                      value={condition.value}
                      onChange={(event) => set(index, { value: event.target.value })}
                      aria-label="Value"
                      className={`${control} w-40`}
                    />
                  )}
                  <button
                    type="button"
                    aria-label="Remove condition"
                    disabled={conditions.length === 1}
                    onClick={() => setConditions(conditions.filter((_, i) => i !== index))}
                    className="rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-30"
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                </li>
              );
            })}
          </ul>
          <button type="button" onClick={() => setConditions([...conditions, blankCondition(fields)])} className="flex w-fit items-center gap-1 text-xs text-accent hover:underline">
            <Plus aria-hidden className="size-3.5" /> Add a condition
          </button>
          <div className="flex items-center gap-2">
            <Button variant="primary" type="button" disabled={!complete} onClick={() => finish(true)}>
              {name.trim() ? "Save and apply" : "Apply"}
            </Button>
            {initial.id && (
              <button
                type="button"
                onClick={() => {
                  onDelete(initial.id);
                  onClose();
                }}
                className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-danger"
              >
                Delete
              </button>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
