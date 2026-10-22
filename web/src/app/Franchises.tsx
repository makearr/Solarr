import { useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Film, LayoutGrid, List, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
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
import { useMediaType } from "@/lib/useMediaTypes";
import { AddToLibrary } from "./work/AddWork";
import { CoverArt } from "./CoverArt";
import { usePreference } from "@/lib/usePreference";
import { LetterRail } from "@/ui/LetterRail";
import { inputStyle } from "@/ui/form";
import { useNavigate } from "@tanstack/react-router";

type Franchise = components["schemas"]["Franchise"];
type View = "poster" | "list";
type Sort = "name" | "items" | "missing" | "newest" | "monitored";

function sortOf(franchises: Franchise[], sort: Sort): Franchise[] {
  const sorted = [...franchises];
  const byName = (a: Franchise, b: Franchise) => a.name.localeCompare(b.name);
  if (sort === "items") sorted.sort((a, b) => (b.total ?? b.members.length) - (a.total ?? a.members.length) || b.members.length - a.members.length || byName(a, b));
  else if (sort === "missing") sorted.sort((a, b) => missingOf(b) - missingOf(a) || byName(a, b));
  else if (sort === "newest") sorted.sort((a, b) => (b.newest_year ?? 0) - (a.newest_year ?? 0) || byName(a, b));
  else if (sort === "monitored") sorted.sort((a, b) => Number(b.monitored) - Number(a.monitored) || byName(a, b));
  else sorted.sort(byName);
  return sorted;
}

/** How many works of a collection the library lacks, as far as the providers say. */
function missingOf(franchise: Franchise): number {
  const held = franchise.members.filter((member) => member.work_id).length;
  return Math.max((franchise.total ?? held) - held, 0);
}

/** "3 / 7 in your library": what the library holds of the works that exist, as far as the providers say. */
export function heldLabel(franchise: Franchise): string {
  const held = franchise.members.filter((member) => member.work_id).length;
  return franchise.total ? t("franchise.held", { held, total: Math.max(franchise.total, held) }) : t("franchise.heldUnknown", { held });
}

/** The letter a name is listed under: its first letter or digit, "#" for the rest. */
function initialOf(name: string): string {
  const letter = name.trim().charAt(0).toLowerCase();
  return /[a-z]/.test(letter) ? letter : "";
}

/** A movie library's collections, as Radarr's: the franchises its films belong to, as posters or a list, monitored as a whole. */
export function FranchiseIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const [view, setView] = usePreference<View>(`franchises.view.${categoryId}`, "poster");
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState<Sort>("name");
  const navigate = useNavigate();
  const [jump, setJump] = useState("");
  const frame = useRef<HTMLDivElement>(null);
  const [creating, setCreating] = useState(false);
  const list = useQuery({
    queryKey: ["franchises", categoryId],
    queryFn: () => ok(api.GET("/franchises", { params: { query: { category_id: Number(categoryId) } } })),
  });
  const monitor = useMutation({
    mutationFn: (franchise: Franchise) => ok(api.POST("/franchises/{id}/monitor", {
      params: { path: { id: franchise.id } },
      body: { category_id: Number(categoryId), monitored: !franchise.monitored },
    })),
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
  const jumpTo = (letter: string) => {
    setJump(letter);
    const target = letter ? frame.current?.querySelector(`[data-initial="${letter}"]`) : undefined;
    if (target) target.scrollIntoView({ block: "start", behavior: "smooth" });
    else frame.current?.scrollTo({ top: 0, behavior: "smooth" });
  };
  return (
    <div className="relative h-full">
      {sort === "name" && <LetterRail value={jump} onChange={jumpTo} />}
      <div ref={frame} className="h-full overflow-y-auto pr-6">
      <div className="mx-auto flex max-w-6xl flex-col gap-4 p-4">
        <PageHeader title={t("franchise.title")} subtitle={t("franchise.help")} />
        <div className="flex flex-wrap items-center gap-2">
          <input className={`${inputStyle} w-56`} placeholder={t("franchise.filter")} value={filter} onChange={(event) => setFilter(event.target.value)} />
          <label className="flex items-center gap-1 text-xs text-ink-muted">
            {t("franchise.sort")}
            <select className={inputStyle} value={sort} onChange={(event) => setSort(event.target.value as Sort)}>
              <option value="name">{t("franchise.sort.name")}</option>
              <option value="items">{t("franchise.sort.items")}</option>
              <option value="missing">{t("franchise.sort.missing")}</option>
              <option value="newest">{t("franchise.sort.newest")}</option>
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
        {creating && <NewCollection categoryId={categoryId} onClose={() => setCreating(false)} onCreated={(id) => { setCreating(false); void navigate({ to: "/library/$categoryId/collections/$franchiseId", params: { categoryId, franchiseId: String(id) } }); }} />}
        {shown.length === 0 && <EmptyState title={t("franchise.none")} />}
        {view === "poster" ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-4">
            {shown.map((franchise) => (
              <Link
                key={franchise.id}
                data-initial={initialOf(franchise.name)}
                to="/library/$categoryId/collections/$franchiseId"
                params={{ categoryId, franchiseId: String(franchise.id) }}
                className="group flex flex-col gap-1 text-left focus-visible:outline-2 focus-visible:outline-accent"
              >
                <span className="block aspect-[2/3] overflow-hidden rounded-lg border border-border-subtle bg-surface">
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
              </Link>
            ))}
          </div>
        ) : (
          <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
            {shown.map((franchise) => (
              <li key={franchise.id} data-initial={initialOf(franchise.name)} className="flex flex-wrap items-center gap-3 px-3 py-2">
                <span className="block h-12 w-8 shrink-0 overflow-hidden rounded border border-border-subtle bg-canvas">
                  {franchise.poster_url && <img src={remoteImage(franchise.poster_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />}
                </span>
                <Link to="/library/$categoryId/collections/$franchiseId" params={{ categoryId, franchiseId: String(franchise.id) }} className="min-w-0 flex-1 truncate text-sm font-medium hover:underline">
                  {franchise.name}
                </Link>
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
      </div>
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
  const mediaType = useMediaType(library?.media_type);
  const [renaming, setRenaming] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [name, setName] = useState(summary.name);
  const [adding, setAdding] = useState<string | undefined>();
  const detail = useQuery({
    queryKey: ["franchise", categoryId, summary.id],
    queryFn: () => ok(api.GET("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } } })),
  });
  const changed = () => {
    void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
    void queryClient.invalidateQueries({ queryKey: ["franchise", categoryId, summary.id] });
  };
  const rename = useMutation({
    mutationFn: async () => {
      await ok(api.PATCH("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } }, body: { name } }));
    },
    onSuccess: () => {
      setRenaming(false);
      changed();
    },
  });
  const remove = useMutation({
    mutationFn: async () => {
      await ok(api.DELETE("/franchises/{id}", { params: { path: { id: summary.id }, query: { category_id: Number(categoryId) } } }));
    },
    onSuccess: () => {
      changed();
      onGone();
    },
  });
  const take = useMutation({
    mutationFn: async (workId: number) => {
      await ok(api.DELETE("/franchises/{id}/members/{workId}", { params: { path: { id: summary.id, workId }, query: { category_id: Number(categoryId) } } }));
    },
    onSuccess: changed,
  });
  const franchise = detail.data ?? summary;
  const custom = franchise.kind === "custom";
  const words = mediaType?.work.many.toLowerCase() ?? "works";
  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="font-medium">{franchise.name}</h2>
        <span className="text-xs text-ink-muted">{heldLabel(franchise)}</span>
        {custom && <Chip>{t("collection.mine")}</Chip>}
        {administrator && (
          <span className="ml-auto flex gap-2">
            {custom && <Button onClick={() => setRenaming(true)}>{t("collection.rename")}</Button>}
            {!custom && (
              <Button busy={monitor.isPending && monitor.variables?.id === franchise.id} onClick={() => monitor.mutate(franchise)}>
                {franchise.monitored ? t("franchise.monitored") : t("franchise.monitor")}
              </Button>
            )}
            <Button variant="icon" aria-label={t("collection.delete")} title={t("collection.delete")} onClick={() => setDeleting(true)}>
              <Trash2 aria-hidden className="size-4" />
            </Button>
          </span>
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
                {administrator && (
                  <button type="button" className="ml-auto text-ink-muted hover:text-ink" title={custom ? t("collection.remove") : t("collection.dismiss")} onClick={() => take.mutate(member.work_id as number)}>
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
          consequence={custom ? t("collection.deleteConsequence") : t("collection.deleteMadeConsequence")}
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
    mutationFn: () => ok(api.POST("/franchises", { body: { category_id: Number(categoryId), name } })),
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

/** One collection on a page of its own: what the library holds of it to open, what it lacks to add. */
export function CollectionPage() {
  const { categoryId, franchiseId } = useParams({ strict: false }) as { categoryId: string; franchiseId: string };
  const administrator = useIsAdministrator();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const detail = useQuery({
    queryKey: ["franchise", categoryId, Number(franchiseId)],
    queryFn: () => ok(api.GET("/franchises/{id}", { params: { path: { id: Number(franchiseId) }, query: { category_id: Number(categoryId) } } })),
  });
  const monitor = useMutation({
    mutationFn: (franchise: Franchise) => ok(api.POST("/franchises/{id}/monitor", {
      params: { path: { id: franchise.id } },
      body: { category_id: Number(categoryId), monitored: !franchise.monitored },
    })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
      void queryClient.invalidateQueries({ queryKey: ["franchise", categoryId, Number(franchiseId)] });
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-6xl flex-col gap-4 p-4">
        <Link to="/library/$categoryId/$level" params={{ categoryId, level: "collections" }} className="text-xs text-ink-muted hover:text-ink">
          ← {t("franchise.title")}
        </Link>
        {detail.isLoading && <p className="text-sm text-ink-muted">{t("work.loading")}</p>}
        {detail.isError && <p className="text-sm text-danger">{t("franchise.failed")}</p>}
        {detail.data && (
          <CollectionDetail
            key={detail.data.id}
            categoryId={categoryId}
            summary={detail.data}
            administrator={administrator}
            monitor={monitor}
            onGone={() => void navigate({ to: "/library/$categoryId/$level", params: { categoryId, level: "collections" } })}
          />
        )}
      </div>
    </div>
  );
}
