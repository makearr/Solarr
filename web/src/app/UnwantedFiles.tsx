import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { Card } from "./SettingsCard";

type ExclusionPattern = components["schemas"]["ExclusionPattern"];
type ExclusionAction = ExclusionPattern["action"];
type BlockedContentEntry = components["schemas"]["BlockedContentEntry"];

const ACTIONS: readonly { id: ExclusionAction; label: string }[] = [
  { id: "ignore", label: "Ignore -- skip silently" },
  { id: "refuse", label: "Refuse -- poisons the whole unit" },
];

/** ADR-53: patterns exclude by shape, hashes block by bytes -- two mechanisms,
 *  deliberately not merged. Both are administrator-only writes; reads are open
 *  to any signed-in caller, the same split Categories/Users already use. */
export function UnwantedFiles() {
  return (
    <>
      <ExclusionPatterns />
      <BlockedContent />
    </>
  );
}

function ExclusionPatterns() {
  const queryClient = useQueryClient();
  const patterns = useQuery({
    queryKey: ["exclusions"],
    queryFn: () => ok(api.GET("/exclusions")),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["exclusions"] });

  const update = useMutation({
    mutationFn: async (pattern: ExclusionPattern) => {
      await ok(api.PATCH("/exclusions/{id}", {
        params: { path: { id: pattern.id } },
        body: {
          pattern: pattern.pattern,
          action: pattern.action,
          media_type: pattern.media_type,
          category_id: pattern.category_id,
          note: pattern.note,
          enabled: pattern.enabled,
        },
      }));
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/exclusions/{id}", { params: { path: { id } } }));
    },
    onSuccess: invalidate,
  });

  if (patterns.error !== null) return null;

  return (
    <Card
      title="Unwanted files"
      help="A file shaped like this is not media: by name or extension, never by content. Shipped defaults stay editable and removable, the same as anything added here."
    >
      {patterns.data && patterns.data.length > 0 ? (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {patterns.data.map((pattern) => (
            <PatternRow
              key={pattern.id}
              pattern={pattern}
              onToggle={() => update.mutate({ ...pattern, enabled: !pattern.enabled })}
              onRemove={() => remove.mutate(pattern.id)}
              busy={update.isPending || remove.isPending}
            />
          ))}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No pattern is configured.
        </p>
      )}

      {(update.isError || remove.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddPattern onAdded={invalidate} />
    </Card>
  );
}

function PatternRow({
  pattern,
  onToggle,
  onRemove,
  busy,
}: {
  pattern: ExclusionPattern;
  onToggle: () => void;
  onRemove: () => void;
  busy: boolean;
}) {
  return (
    <li className="flex flex-wrap items-center gap-2 px-3 py-2.5">
      <span className="font-mono text-sm">{pattern.pattern}</span>
      <span
        className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
          pattern.action === "refuse"
            ? "border-danger text-danger"
            : "border-border-subtle text-ink-muted"
        }`}
      >
        {pattern.action}
      </span>
      {pattern.media_type && (
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem]
                         text-ink-muted">
          {mediaTypeName(pattern.media_type)}
        </span>
      )}
      {pattern.builtin && (
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem]
                         text-ink-muted">
          shipped default
        </span>
      )}
      {pattern.note && <span className="text-xs text-ink-muted">{pattern.note}</span>}

      <label className="ml-auto flex shrink-0 items-center gap-1.5 text-xs text-ink-muted">
        <input
          type="checkbox"
          checked={pattern.enabled}
          disabled={busy}
          onChange={onToggle}
          className="size-3.5"
        />
        enabled
      </label>
      <button
        type="button"
        aria-label={`Remove pattern ${pattern.pattern}`}
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

function AddPattern({ onAdded }: { onAdded: () => void }) {
  const [pattern, setPattern] = useState("");
  const [action, setAction] = useState<ExclusionAction>("ignore");
  const [note, setNote] = useState("");

  const create = useMutation({
    mutationFn: () => ok(api.POST("/exclusions", {
      body: { pattern, action, note: note || undefined, enabled: true },
    })),
    onSuccess: () => {
      setPattern("");
      setNote("");
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
        Pattern
        <input
          className={inputStyle}
          value={pattern}
          placeholder="*.exe"
          autoComplete="off"
          onChange={(event) => setPattern(event.target.value)}
        />
      </label>
      <label className={labelStyle}>
        Action
        <select
          className={inputStyle}
          value={action}
          onChange={(event) => setAction(event.target.value as ExclusionAction)}
        >
          {ACTIONS.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.label}
            </option>
          ))}
        </select>
      </label>
      <label className="flex min-w-40 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Note
        <input
          className={inputStyle}
          value={note}
          placeholder="Why this pattern exists"
          autoComplete="off"
          onChange={(event) => setNote(event.target.value)}
        />
      </label>
      <Button
        variant="primary"
        type="submit"
        disabled={!pattern.trim() || create.isPending}
      >
        {create.isPending ? "Adding…" : "Add pattern"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That pattern could not be created. Check the pattern and action are valid.
        </p>
      )}
    </form>
  );
}

function BlockedContent() {
  const queryClient = useQueryClient();
  const blocked = useQuery({
    queryKey: ["blocked"],
    queryFn: () => ok(api.GET("/blocked")),
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/blocked/{id}", { params: { path: { id } } }));
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["blocked"] }),
  });

  if (blocked.error !== null) return null;

  return (
    <Card
      title="Blocked content"
      help={'Bytes that never enter the library again, keyed on content hashes, never on a filename. Made by "Remove and block" on a file.'}
    >
      {blocked.data && blocked.data.length > 0 ? (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {blocked.data.map((entry) => (
            <BlockedRow
              key={entry.id}
              entry={entry}
              onRemove={() => entry.id !== undefined && remove.mutate(entry.id)}
              busy={remove.isPending}
            />
          ))}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          Nothing is blocked.
        </p>
      )}

      {remove.isError && <p className="text-xs text-danger">That block could not be removed.</p>}
    </Card>
  );
}

function BlockedRow({
  entry,
  onRemove,
  busy,
}: {
  entry: BlockedContentEntry;
  onRemove: () => void;
  busy: boolean;
}) {
  const hash = entry.oshash ?? entry.quick_hash ?? entry.chromaprint ?? entry.phash ?? "";
  return (
    <li className="flex flex-wrap items-center gap-2 px-3 py-2.5">
      <span className="text-sm font-medium">{entry.title || "(untitled)"}</span>
      <span className="rounded-full border border-danger px-1.5 py-px text-[0.6875rem] text-danger">
        {entry.reason}
      </span>
      <span className="truncate font-mono text-[0.6875rem] text-ink-muted" title={hash}>
        {hash.slice(0, 16)}
        {hash.length > 16 ? "…" : ""}
      </span>
      {entry.hit_count !== undefined && entry.hit_count > 0 && (
        <span className="text-xs text-ink-muted">
          fired {entry.hit_count}×
        </span>
      )}
      <button
        type="button"
        aria-label={`Remove block ${entry.title ?? entry.id}`}
        onClick={onRemove}
        disabled={busy}
        className="ml-auto shrink-0 rounded-md border border-border-subtle p-1 text-ink-muted
                   hover:text-danger disabled:opacity-50 focus-visible:outline-2
                   focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </button>
    </li>
  );
}
