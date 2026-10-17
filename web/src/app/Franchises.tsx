import { useMemo, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Film, LayoutGrid, List, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";
import { t } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { PageHeader } from "@/ui/PageHeader";
import { ConfirmDialog } from "@/ui/ConfirmDialog";
import { Dialog } from "@/ui/Dialog";
import { useLibrary } from "@/lib/useLibrary";
import { AddToLibrary } from "./work/AddWork";
import { CoverArt } from "./CoverArt";
import { usePreference } from "@/lib/usePreference";

type Franchise = components["schemas"]["Franchise"];
type View = "poster" | "list";
type Sort = "name" | "held" | "monitored";

const inputStyle = "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink";

function sortOf(franchises: Franchise[], sort: Sort): Franchise[] {
  const sorted = [...franchises];
  if (sort === "held") sorted.sort((a, b) => b.members.length - a.members.length || a.name.localeCompare(b.name));
  else if (sort === "monitored") sorted.sort((a, b) => Number(b.monitored) - Number(a.monitored) || a.name.localeCompare(b.name));
  else sorted.sort((a, b) => a.name.localeCompare(b.name));
  return sorted;
}

function heldLabel(franchise: Franchise): string {
  return franchise.total ? t("franchise.held", { held: franchise.members.length, total: franchise.total }) : t("franchise.heldUnknown", { held: franchise.members.length });
}

/** A movie library's collections, as Radarr's: the franchises its films belong to, as posters or a list, monitored as a whole. */
export function FranchiseIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const [view, setView] = usePreference<View>(`franchises.view.${categoryId}`, "poster");
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState<Sort>("name");
  const [open, setOpen] = useState<number | undefined>();
  const [creating, setCreating] = useState(false);
  const list = useQuery({
    queryKey: ["franchises", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/franchises", { params: { query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const monitor = useMutation({
    mutationFn: async (franchise: Franchise) => {
      const { data, error } = await api.POST("/franchises/{id}/monitor", {
        params: { path: { id: franchise.id } },
        body: { category_id: Number(categoryId), monitored: !franchise.monitored },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });
  const shown = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const all = list.data?.franchises ?? [];
    return sortOf(needle ? all.filter((franchise) => franchise.name.toLowerCase().includes(needle)) : all, sort);
  }, [list.data, filter, sort]);
  if (list.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>;
  if (!list.data) return <p className="p-6 text-sm text-danger">{t("franchise.failed")}</p>;
  const opened = shown.find((franchise) => franchise.id === open);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-6xl flex-col gap-4 p-4">
        <PageHeader title={t("franchise.title")} subtitle={t("franchise.help")} />
        <div className="flex flex-wrap items-center gap-2">
          <input className={`${inputStyle} w-56`} placeholder={t("franchise.filter")} value={filter} onChange={(event) => setFilter(event.target.value)} />
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            {t("franchise.sort")}
            <select className={inputStyle} value={sort} onChange={(event) => setSort(event.target.value as Sort)}>
              <option value="name">{t("franchise.sort.name")}</option>
              <option value="held">{t("franchise.sort.held")}</option>
              <option value="monitored">{t("franchise.sort.monitored")}</option>
            </select>
          </label>
          <span className="ml-auto flex items-center gap-1">
            {administrator && <Button onClick={() => setCreating(true)}>{t("collection.new")}</Button>}
            <Button variant="icon" aria-label={t("franchise.view.poster")} aria-pressed={view === "poster"} onClick={() => setView("poster")}>
              <LayoutGrid aria-hidden className="size-4" />
            </Button>
            <Button variant="icon" aria-label={t("franchise.view.list")} aria-pressed={view === "list"} onClick={() => setView("list")}>
              <List aria-hidden className="size-4" />
            </Button>
          </span>
        </div>
        {creating && <NewCollection categoryId={categoryId} onClose={() => setCreating(false)} onCreated={(id) => { setCreating(false); setOpen(id); }} />}
        {shown.length === 0 && <EmptyState title={t("franchise.none")} />}
        {view === "poster" ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-4">
            {shown.map((franchise) => (
              <button
                key={franchise.id}
                type="button"
                aria-pressed={open === franchise.id}
                onClick={() => setOpen(open === franchise.id ? undefined : franchise.id)}
                className="group flex flex-col gap-1 text-left focus-visible:outline-2 focus-visible:outline-accent"
              >
                <span className={`block aspect-[2/3] overflow-hidden rounded-lg border bg-surface ${open === franchise.id ? "border-accent" : "border-border-subtle"}`}>
                  {franchise.poster_url ? (
                    <img src={remoteImage(franchise.poster_url, "small")} alt="" loading="lazy" className="size-full object-cover" />
                  ) : (
                    <span className="flex size-full items-center justify-center text-ink-muted">
                      <Film aria-hidden className="size-8" />
                    </span>
                  )}
                </span>
                <span className="truncate text-sm font-medium">{franchise.name}</span>
                <span className="truncate text-xs text-ink-muted">{heldLabel(franchise)}</span>
                {franchise.monitored && <Chip tone="ok">{t("franchise.monitored")}</Chip>}
              </button>
            ))}
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
            {shown.map((franchise) => (
              <li key={franchise.id} className="flex flex-wrap items-center gap-3 px-3 py-2">
                <span className="block h-12 w-8 shrink-0 overflow-hidden rounded border border-border-subtle bg-canvas">
                  {franchise.poster_url && <img src={remoteImage(franchise.poster_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />}
                </span>
                <button type="button" onClick={() => setOpen(open === franchise.id ? undefined : franchise.id)} className="min-w-0 flex-1 truncate text-left text-sm font-medium hover:underline">
                  {franchise.name}
                </button>
                <span className="text-xs text-ink-muted">{heldLabel(franchise)}</span>
                {franchise.monitored && <Chip tone="ok">{t("franchise.monitored")}</Chip>}
                {administrator && (
                  <Button busy={monitor.isPending && monitor.variables?.id === franchise.id} onClick={() => monitor.mutate(franchise)}>
                    {franchise.monitored ? t("franchise.monitored") : t("franchise.monitor")}
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        {opened && (
          <CollectionDetail
            key={opened.id}
            categoryId={categoryId}
            summary={opened}
            administrator={administrator}
            monitor={monitor}
            onGone={() => setOpen(undefined)}
          />
        )}
      </div>
    </div>
  );
}

/** One collection: the works it holds to open, the ones the provider lists that the library lacks to add, and for a
 *  collection made by hand its name and what is in it. */
function CollectionDetail({
  categoryId, summary, administrator, monitor, onGone,
}: {
  categoryId: string;
  summary: Franchise;
  administrator: boolean;
  monitor: { mutate: (franchise: Franchise) => void; isPending: boolean; variables?: Franchise; isSuccess: boolean; data?: { added: number } };
  onGone: () => void;
}) {
  const queryClient = useQueryClient();
  const library = useLibrary(categoryId);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [name, setName] = useState(summary.name);
  const [adding, setAdding] = useState<string | undefined>();
  const detail = useQuery({
    queryKey: ["franchise", categoryId, summary.id],
    queryFn: async () => {
      const { data, error } = await api.GET("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
    void queryClient.invalidateQueries({ queryKey: ["franchise", categoryId, summary.id] });
  };
  const rename = useMutation({
    mutationFn: async () => {
      const { error } = await api.PATCH("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } }, body: { name } });
      if (error) throw error;
    },
    onSuccess: () => {
      setRenaming(false);
      changed();
    },
  });
  const remove = useMutation({
    mutationFn: async () => {
      const { error } = await api.DELETE("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
    },
    onSuccess: () => {
      changed();
      onGone();
    },
  });
  const take = useMutation({
    mutationFn: async (workId: number) => {
      const { error } = await api.DELETE("/franchises/{id}/members/{workId}", { params: { path: { id: summary.id, workId }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
    },
    onSuccess: changed,
  });
  const franchise = detail.data ?? summary;
  const custom = franchise.kind === "custom";
  const words = library?.media_type === "movie" ? "films" : "works";
  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="font-medium">{franchise.name}</h2>
        <span className="text-xs text-ink-muted">{heldLabel(franchise)}</span>
        {custom && <Chip>{t("collection.mine")}</Chip>}
        {administrator && custom && (
          <span className="ml-auto flex gap-2">
            <Button onClick={() => setRenaming(true)}>{t("collection.rename")}</Button>
            <Button variant="icon" aria-label={t("collection.delete")} title={t("collection.delete")} onClick={() => setDeleting(true)}>
              <Trash2 aria-hidden className="size-4" />
            </Button>
          </span>
        )}
        {administrator && !custom && (
          <Button className="ml-auto" busy={monitor.isPending && monitor.variables?.id === franchise.id} onClick={() => monitor.mutate(franchise)}>
            {franchise.monitored ? t("franchise.monitored") : t("franchise.monitor")}
          </Button>
        )}
      </div>
      {monitor.isSuccess && monitor.variables?.id === franchise.id && !custom && (
        <p className="text-xs text-ink-muted">{monitor.data?.added ? t("franchise.added", { count: monitor.data.added }) : franchise.monitored ? "" : t("franchise.noKey")}</p>
      )}
      {franchise.members.length === 0 && <p className="text-sm text-ink-muted">{t("collection.empty")}</p>}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-4">
        {franchise.members.map((member) => {
          const key = member.work_id ? `w${member.work_id}` : `${member.provider}:${member.external_id}`;
          const caption = (
            <>
              <span className="block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-canvas">
                {member.poster_url ? (
                  <img src={remoteImage(member.poster_url, "small")} alt="" loading="lazy" className={`size-full object-cover ${member.work_id ? "" : "opacity-60"}`} />
                ) : member.work_id ? (
                  <CoverArt workId={member.work_id} title={member.title} size="small" className="size-full object-cover" />
                ) : (
                  <span className="flex size-full items-center justify-center text-ink-muted">
                    <Film aria-hidden className="size-8" />
                  </span>
                )}
              </span>
              <span className="truncate text-sm font-medium">
                {member.title}
                {member.year ? <span className="font-normal text-ink-muted"> ({member.year})</span> : null}
              </span>
            </>
          );
          return member.work_id ? (
            <div key={key} className="flex flex-col gap-1">
              <Link to="/library/$categoryId/works/$workId/$tab" params={{ categoryId, workId: String(member.work_id), tab: "overview" }} className="flex flex-col gap-1 focus-visible:outline-2 focus-visible:outline-accent">
                {caption}
              </Link>
              <span className="flex items-center gap-2 text-xs">
                <span className={member.has_file ? "text-ok" : member.monitored ? "text-danger" : "text-ink-muted"}>
                  {member.has_file ? t("franchise.onDisk") : member.monitored ? t("franchise.missing") : t("franchise.unmonitored")}
                </span>
                {administrator && custom && (
                  <button type="button" className="ml-auto text-ink-muted hover:text-ink" title={t("collection.remove")} onClick={() => take.mutate(member.work_id as number)}>
                    ✕
                  </button>
                )}
              </span>
            </div>
          ) : (
            <div key={key} className="flex flex-col gap-1">
              {caption}
              {member.provider && member.external_id && (
                <>
                  <button
                    type="button"
                    aria-expanded={adding === key}
                    onClick={() => setAdding(adding === key ? undefined : key)}
                    className="w-fit rounded-md border border-border-subtle px-2 py-0.5 text-xs text-accent hover:border-accent"
                  >
                    {t("collection.request")}
                  </button>
                  {adding === key && (
                    <AddToLibrary
                      candidate={{ provider: member.provider, external_id: member.external_id, title: member.title }}
                      categoryId={categoryId}
                      onDone={() => {
                        setAdding(undefined);
                        changed();
                      }}
                    />
                  )}
                </>
              )}
            </div>
          );
        })}
      </div>
      <p className="text-[11px] text-ink-muted">{words}</p>
      {renaming && (
        <Dialog
          title={t("collection.rename")}
          onClose={() => setRenaming(false)}
          actions={
            <>
              <Button onClick={() => setRenaming(false)}>{t("action.cancel")}</Button>
              <Button variant="primary" busy={rename.isPending} disabled={!name.trim()} onClick={() => rename.mutate()}>
                {t("collection.rename")}
              </Button>
            </>
          }
        >
          <input className={inputStyle + " w-full"} value={name} onChange={(event) => setName(event.target.value)} />
        </Dialog>
      )}
      {deleting && (
        <ConfirmDialog
          title={t("collection.delete")}
          consequence={t("collection.deleteConsequence")}
          confirmLabel={t("collection.delete")}
          danger
          busy={remove.isPending}
          onCancel={() => setDeleting(false)}
          onConfirm={() => remove.mutate()}
        />
      )}
    </section>
  );
}

function NewCollection({ categoryId, onClose, onCreated }: { categoryId: string; onClose: () => void; onCreated: (id: number) => void }) {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/franchises", { body: { category_id: Number(categoryId), name } });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => {
      void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
      onCreated(data.id);
    },
  });
  return (
    <Dialog
      title={t("collection.new")}
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>{t("action.cancel")}</Button>
          <Button variant="primary" busy={create.isPending} disabled={!name.trim()} onClick={() => create.mutate()}>
            {t("collection.new")}
          </Button>
        </>
      }
    >
      <input className={inputStyle + " w-full"} value={name} onChange={(event) => setName(event.target.value)} placeholder={t("collection.add.placeholder")} />
    </Dialog>
  );
}
