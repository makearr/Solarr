import { useMemo, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Film, LayoutGrid, List } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { remoteImage } from "@/lib/remoteImage";
import { t } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { PageHeader } from "@/ui/PageHeader";
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
          <span className="ml-auto flex gap-1">
            <Button variant="icon" aria-label={t("franchise.view.poster")} aria-pressed={view === "poster"} onClick={() => setView("poster")}>
              <LayoutGrid aria-hidden className="size-4" />
            </Button>
            <Button variant="icon" aria-label={t("franchise.view.list")} aria-pressed={view === "list"} onClick={() => setView("list")}>
              <List aria-hidden className="size-4" />
            </Button>
          </span>
        </div>
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
          <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-center gap-3">
              <h2 className="font-medium">{opened.name}</h2>
              <span className="text-xs text-ink-muted">{heldLabel(opened)}</span>
              {administrator && (
                <Button className="ml-auto" busy={monitor.isPending && monitor.variables?.id === opened.id} onClick={() => monitor.mutate(opened)}>
                  {opened.monitored ? t("franchise.monitored") : t("franchise.monitor")}
                </Button>
              )}
            </div>
            {monitor.isSuccess && monitor.variables?.id === opened.id && (
              <p className="text-xs text-ink-muted">{monitor.data?.added ? t("franchise.added", { count: monitor.data.added }) : opened.monitored ? "" : t("franchise.noKey")}</p>
            )}
            <ul className="flex flex-col divide-y divide-border-subtle text-sm">
              {opened.members.map((member) => (
                <li key={member.work_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5">
                  <Link to="/library/$categoryId/works/$workId/$tab" params={{ categoryId, workId: String(member.work_id), tab: "overview" }} className="hover:underline">
                    {member.title}
                  </Link>
                  {member.year ? <span className="text-xs text-ink-muted">{member.year}</span> : null}
                  <span className={`ml-auto text-xs ${member.has_file ? "text-ok" : member.monitored ? "text-danger" : "text-ink-muted"}`}>
                    {member.has_file ? t("franchise.onDisk") : member.monitored ? t("franchise.missing") : t("franchise.unmonitored")}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </div>
  );
}
