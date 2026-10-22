import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

type ImportList = components["schemas"]["ImportList"];

/** ADR-38, todo 4.16: one library's own *Arr-shaped import lists. Scaffolding
 *  only -- nothing syncs against one yet, that is stage 12 -- so this screen
 *  is naming-and-policy CRUD, the same shape UnwantedFiles' ExclusionPatterns
 *  already established. type is deliberately free-form (confirmed with the
 *  user 2026-09-09): no music-specific list type is baked in ahead of a real
 *  provider integration to name one against. */
export function ImportLists() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const queryClient = useQueryClient();

  const lists = useQuery({
    queryKey: ["importLists", id],
    enabled: id !== undefined,
    queryFn: () => ok(api.GET("/libraries/{id}/import-lists", {
      params: { path: { id: id! } },
    })),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["importLists", id] });

  const update = useMutation({
    mutationFn: async (list: ImportList) => {
      await ok(api.PATCH("/import-lists/{id}", {
        params: { path: { id: list.id } },
        body: {
          name: list.name,
          type: list.type,
          url: list.url,
          cadence_hours: list.cadence_hours,
          monitor: list.monitor,
          quality_profile_id: list.quality_profile_id,
          exclusions: list.exclusions,
          enabled: list.enabled,
        },
      }));
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (listId: number) => {
      await ok(api.DELETE("/import-lists/{id}", { params: { path: { id: listId } } }));
    },
    onSuccess: invalidate,
  });

  if (id === undefined) return null;

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">Import lists</h1>
      <p className="text-sm leading-relaxed text-ink-muted">
        *Arr-shaped list sources for this library. Nothing syncs against one yet (stage 12) --
        adding a list records intent, it does not fetch anything.
      </p>

      {lists.data && lists.data.length > 0 ? (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {lists.data.map((list) => (
            <ListRow
              key={list.id}
              list={list}
              onToggleEnabled={() => update.mutate({ ...list, enabled: !list.enabled })}
              onToggleMonitor={() => update.mutate({ ...list, monitor: !list.monitor })}
              onRemove={() => remove.mutate(list.id)}
              busy={update.isPending || remove.isPending}
            />
          ))}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No import list is configured for this library.
        </p>
      )}

      {(update.isError || remove.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddList libraryId={id} onAdded={invalidate} />
    </div>
  );
}

function ListRow({
  list,
  onToggleEnabled,
  onToggleMonitor,
  onRemove,
  busy,
}: {
  list: ImportList;
  onToggleEnabled: () => void;
  onToggleMonitor: () => void;
  onRemove: () => void;
  busy: boolean;
}) {
  return (
    <li className="flex flex-wrap items-center gap-2 px-3 py-2.5">
      <span className="text-sm font-medium">{list.name}</span>
      <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
        {list.type}
      </span>
      {list.url && (
        <span className="truncate font-mono text-[0.6875rem] text-ink-muted" title={list.url}>
          {list.url}
        </span>
      )}
      <span className="text-xs text-ink-muted">every {list.cadence_hours}h</span>

      <label className="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-ink-muted">
        <input
          type="checkbox"
          checked={list.monitor}
          disabled={busy}
          onChange={onToggleMonitor}
          className="size-3.5"
        />
        monitor
      </label>
      <label className="flex shrink-0 items-center gap-1.5 text-xs text-ink-muted">
        <input
          type="checkbox"
          checked={list.enabled}
          disabled={busy}
          onChange={onToggleEnabled}
          className="size-3.5"
        />
        enabled
      </label>
      <button
        type="button"
        aria-label={`Remove import list ${list.name}`}
        onClick={onRemove}
        disabled={busy}
        className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </button>
    </li>
  );
}

function AddList({ libraryId, onAdded }: { libraryId: number; onAdded: () => void }) {
  const [name, setName] = useState("");
  const [type, setType] = useState("");
  const [url, setUrl] = useState("");
  const [cadenceHours, setCadenceHours] = useState(24);

  const create = useMutation({
    mutationFn: () => ok(api.POST("/libraries/{id}/import-lists", {
      params: { path: { id: libraryId } },
      body: { name, type, url: url || undefined, cadence_hours: cadenceHours, monitor: true, enabled: true },
    })),
    onSuccess: () => {
      setName("");
      setType("");
      setUrl("");
      setCadenceHours(24);
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          placeholder="My discography list"
          autoComplete="off"
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex min-w-28 flex-col gap-1 text-xs text-ink-muted">
        Type
        <input
          className={inputStyle}
          value={type}
          placeholder="e.g. headphones"
          autoComplete="off"
          onChange={(event) => setType(event.target.value)}
        />
      </label>
      <label className="flex min-w-40 flex-1 flex-col gap-1 text-xs text-ink-muted">
        URL
        <input
          className={inputStyle}
          value={url}
          placeholder="https://…"
          autoComplete="off"
          onChange={(event) => setUrl(event.target.value)}
        />
      </label>
      <label className="flex w-24 flex-col gap-1 text-xs text-ink-muted">
        Cadence (h)
        <input
          className={inputStyle}
          type="number"
          min={1}
          value={cadenceHours}
          onChange={(event) => setCadenceHours(Number(event.target.value))}
        />
      </label>
      <Button
        variant="primary"
        type="submit"
        disabled={!name.trim() || !type.trim() || create.isPending}
      >
        {create.isPending ? "Adding…" : "Add list"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That import list could not be created. Check the name, type and cadence are valid.
        </p>
      )}
    </form>
  );
}
