import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Play, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, vocabularyOf } from "@/lib/i18n";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaType } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { ConfirmDialog } from "@/ui/ConfirmDialog";

import { ImportListForm, ImportListPreview, writeOf } from "./ImportListDialogs";

type ImportList = components["schemas"]["ImportList"];
type ImportListTypes = components["schemas"]["ImportListTypes"];

/** ADR-38: one library's import lists, Sonarr's Import Lists screen: each previewed before it is enabled, then synced
 *  on its own cadence. documentation/import-lists.md. */
export function ImportLists() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const library = useLibrary(categoryId);
  const words = vocabularyOf(useMediaType(library?.media_type));
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState<ImportList | "new" | null>(null);
  const [previewing, setPreviewing] = useState<ImportList | null>(null);
  const [removing, setRemoving] = useState<ImportList | null>(null);

  const lists = useQuery({
    queryKey: ["importLists", id],
    enabled: id !== undefined,
    queryFn: () => ok(api.GET("/libraries/{id}/import-lists", { params: { path: { id: id! } } })),
  });
  const types = useQuery({
    queryKey: ["importListTypes", id],
    enabled: id !== undefined,
    staleTime: Infinity,
    queryFn: () => ok(api.GET("/libraries/{id}/import-list-types", { params: { path: { id: id! } } })),
  });
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["importLists", id] });

  const setEnabled = useMutation({
    mutationFn: (args: { list: ImportList; enabled: boolean }) =>
      ok(api.PATCH("/import-lists/{id}", { params: { path: { id: args.list.id } }, body: { ...writeOf(args.list), enabled: args.enabled } })),
    onSuccess: invalidate,
  });
  const run = useMutation({
    mutationFn: (listId: number) => ok(api.POST("/import-lists/{id}/run", { params: { path: { id: listId } } })),
  });
  const remove = useMutation({
    mutationFn: (listId: number) => ok(api.DELETE("/import-lists/{id}", { params: { path: { id: listId } } })),
    onSuccess: () => {
      setRemoving(null);
      void invalidate();
    },
  });

  if (id === undefined) return null;
  const takesLists = (types.data?.types.length ?? 0) > 0;

  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-wrap items-start gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <h1 className="text-lg font-semibold">{t("importLists.title")}</h1>
          <p className="text-sm leading-relaxed text-ink-muted">{t("importLists.help", words)}</p>
        </div>
        {takesLists && (
          <Button variant="primary" onClick={() => setEditing("new")}>
            {t("importLists.add")}
          </Button>
        )}
      </header>

      {lists.isLoading && <p className="text-sm text-ink-muted">{t("importLists.loading")}</p>}
      {lists.isError && <p className="text-sm text-danger">{t("importLists.readFailed")}</p>}
      {types.data && !takesLists && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">{t("importLists.noTypes")}</p>
      )}

      {lists.data && lists.data.length > 0 && (
        <ul className="flex flex-col gap-2">
          {lists.data.map((list) => (
            <ListRow
              key={list.id}
              list={list}
              types={types.data}
              busy={setEnabled.isPending}
              queued={run.isSuccess && run.variables === list.id}
              onPreview={() => setPreviewing(list)}
              onEdit={() => setEditing(list)}
              onRemove={() => setRemoving(list)}
              onRun={() => run.mutate(list.id)}
              onEnabled={(enabled) => (enabled && !list.previewed ? setPreviewing(list) : setEnabled.mutate({ list, enabled }))}
            />
          ))}
        </ul>
      )}
      {lists.data && lists.data.length === 0 && takesLists && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">{t("importLists.none")}</p>
      )}
      {(setEnabled.isError || run.isError || remove.isError) && <p className="text-xs text-danger">{t("importLists.refused")}</p>}

      {editing !== null && types.data && (
        <ImportListForm
          libraryId={id}
          mediaType={library?.media_type}
          list={editing === "new" ? undefined : editing}
          types={types.data}
          onClose={() => setEditing(null)}
          onSaved={(saved, preview) => {
            setEditing(null);
            void invalidate();
            if (preview) setPreviewing(saved);
          }}
        />
      )}
      {previewing && (
        <ImportListPreview
          list={previewing}
          onClose={() => {
            setPreviewing(null);
            void invalidate();
          }}
        />
      )}
      {removing && (
        <ConfirmDialog
          title={t("importLists.remove.title", { name: removing.name })}
          consequence={t("importLists.remove.consequence")}
          confirmLabel={t("importLists.remove")}
          cancelLabel={t("importLists.form.cancel")}
          danger
          busy={remove.isPending}
          onConfirm={() => remove.mutate(removing.id)}
          onCancel={() => setRemoving(null)}
        />
      )}
    </div>
  );
}

function ListRow({
  list,
  types,
  busy,
  queued,
  onPreview,
  onEdit,
  onRemove,
  onRun,
  onEnabled,
}: {
  list: ImportList;
  types: ImportListTypes | undefined;
  busy: boolean;
  queued: boolean;
  onPreview: () => void;
  onEdit: () => void;
  onRemove: () => void;
  onRun: () => void;
  onEnabled: (enabled: boolean) => void;
}) {
  const type = types?.types.find((candidate) => candidate.type === list.type);
  const settings = (type?.fields ?? [])
    .map((field) => {
      const value = list.options[field.key] ?? field.default;
      if (!value) return "";
      const labels = value.split(",").map((key) => field.choices.find((choice) => choice.key === key)?.label ?? key);
      return `${field.label}: ${labels.join(", ")}`;
    })
    .filter(Boolean)
    .join(" · ");

  return (
    <li className="flex flex-col gap-1.5 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{list.name}</span>
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
          {type?.label ?? list.type}
        </span>
        <span className="text-xs text-ink-muted">{t("importLists.every", { hours: list.cadence_hours })}</span>
        <span className="ml-auto flex shrink-0 flex-wrap items-center gap-1.5">
          <StatusPill list={list} />
          <Button variant="secondary" className="!px-2 !py-0.5 text-xs" onClick={onPreview}>
            {t("importLists.preview")}
          </Button>
          {list.enabled ? (
            <Button variant="secondary" className="!px-2 !py-0.5 text-xs" disabled={busy} onClick={() => onEnabled(false)}>
              {t("importLists.disable")}
            </Button>
          ) : (
            <Button variant="secondary" className="!px-2 !py-0.5 text-xs" disabled={busy} onClick={() => onEnabled(true)}>
              {list.previewed ? t("importLists.enable") : t("importLists.needsPreview")}
            </Button>
          )}
          <Button
            variant="icon"
            aria-label={t("importLists.runNow")}
            title={queued ? t("importLists.queued") : t("importLists.runNow")}
            disabled={!list.enabled || queued}
            onClick={onRun}
            icon={<Play aria-hidden className="size-3.5" />}
          />
          <Button variant="icon" aria-label={t("importLists.edit")} title={t("importLists.edit")} onClick={onEdit}
            icon={<Pencil aria-hidden className="size-3.5" />} />
          <Button variant="icon" aria-label={t("importLists.remove")} title={t("importLists.remove")} onClick={onRemove}
            className="hover:!text-danger" icon={<Trash2 aria-hidden className="size-3.5" />} />
        </span>
      </div>
      {settings && <span className="text-xs text-ink-muted">{settings}</span>}
      <LastRun list={list} />
    </li>
  );
}

function StatusPill({ list }: { list: ImportList }) {
  const enabled = list.enabled;
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        enabled ? "border-ok text-ok" : "border-border-subtle text-ink-muted"
      }`}
    >
      {enabled ? t("importLists.enabled") : t("importLists.disabled")}
    </span>
  );
}

function LastRun({ list }: { list: ImportList }) {
  if (!list.last_run_at) return <span className="text-xs text-ink-muted">{t("importLists.neverRun")}</span>;
  const when = new Date(list.last_run_at).toLocaleString();
  if (list.last_run_failure) {
    return (
      <span className="text-xs text-danger">
        {t("importLists.lastRunFailed", { when, failure: t(`importLists.failure.${list.last_run_failure}`) })}
      </span>
    );
  }
  return <span className="text-xs text-ink-muted">{t("importLists.lastRun", { when, added: list.last_run_added })}</span>;
}
