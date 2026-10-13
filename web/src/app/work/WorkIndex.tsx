import { useMemo, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { albumStatusLabel } from "@/lib/albumStatus";
import { t, vocabularyOf } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { usePreference } from "@/lib/usePreference";
import { whereWithTitle, type FilterField } from "@/list/customFilter";
import { ListTable, type Column } from "@/list/ListTable";
import { PosterGrid } from "@/list/PosterGrid";
import type { SelectionRequest } from "@/list/selection";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import { useNarrow } from "@/list/useNarrow";
import { useRecordLastLevel } from "@/list/useRecordLastLevel";
import { useRouteView, type ViewState } from "@/list/useRouteView";
import { Button } from "@/ui/Button";
import { ConfirmDialog } from "@/ui/ConfirmDialog";
import { EmptyState } from "@/ui/EmptyState";
import { Image } from "@/ui/Image";
import { Toolbar, ToolbarAdd, ToolbarSearch, ToolbarSort } from "@/ui/Toolbar";

import { CustomFilterMenu } from "../CustomFilterMenu";
import { STATUS_CHOICES } from "../filterFields";
import {
  DEFAULT_POSTER_OPTIONS, IndexPoster, IndexViewToggle, OVERVIEW_ROW_HEIGHT, OverviewList, POSTER_GAP, POSTER_PADDING,
  POSTER_WIDTHS, PosterOptionsMenu, RowActions, posterRowHeight, workEntry, type IndexView, type PosterOptions,
} from "../IndexViews";
import { formatBytes } from "../Queue";
import { BulkEditWork } from "./EditWork";
import { formatMinutes } from "./workFormat";

type Work = components["schemas"]["Work"];

// Filters that ride in the URL; the library is the route's own.
const FILTER_KEYS: readonly string[] = ["fresh", "q", "where"];
const CARD_HEIGHT = 68;
const TABLE_ROW_HEIGHT = 44;
const GROUP_HEADER_HEIGHT = 44;

// The base fields every work has; a type's facet of the same key replaces one.
const BASE_FIELDS: readonly FilterField[] = [
  { key: "title", label: "Title", kind: "text" },
  { key: "year", label: "Year", kind: "number" },
  { key: "tag", label: "Tag", kind: "text" },
  { key: "status", label: "Status", kind: "enum", choices: STATUS_CHOICES },
  { key: "monitored", label: "Monitored", kind: "bool" },
  { key: "on_disk", label: "Has files", kind: "bool" },
  { key: "verified", label: "Identified", kind: "bool" },
  { key: "size_mb", label: "Size on disk (MB)", kind: "number" },
];

/** What a custom filter may name for a type: the base fields, the lead where there is one, and its own facets. */
export function filterFieldsOf(info: MediaTypeInfo | undefined): FilterField[] {
  const lead = info?.roles.find((role) => role.lead);
  const fields: FilterField[] = [...BASE_FIELDS, ...(lead ? [{ key: "artist", label: lead.label, kind: "text" as const }] : [])];
  for (const facet of info?.facets ?? []) {
    if (!facet.filterable || facet.entity !== "work") continue;
    const field: FilterField =
      facet.kind === "enum"
        ? { key: facet.key, label: facet.label, kind: "enum", choices: (facet.choices ?? []).map((choice) => [choice.key, choice.label] as const) }
        : { key: facet.key, label: facet.label, kind: facet.kind === "number" || facet.kind === "duration" ? "number" : "text" };
    const at = fields.findIndex((existing) => existing.key === facet.key);
    if (at >= 0) fields[at] = field;
    else fields.push(field);
  }
  return fields;
}

/** What the list can be ordered by: the base orders, the lead where there is one, and the type's sortable facets. */
export function sortChoicesOf(info: MediaTypeInfo | undefined): [string, string][] {
  const lead = info?.roles.find((role) => role.lead);
  const choices: [string, string][] = [
    ...(lead ? [["artist", lead.label] as [string, string]] : []),
    ["title", "Title"],
    ["year", "Year"],
    ["added", "Added"],
    ["size", "Size on disk"],
    ["profile", "Quality profile"],
    ["status", "Availability"],
    ["tag", "Tags"],
  ];
  for (const facet of info?.facets ?? []) {
    if (facet.sortable && facet.entity === "work" && facet.key !== "year") choices.push([`facet:${facet.key}`, facet.label]);
  }
  return choices;
}

// Selection is a query, never a fetched list of ids (section 21.7).
function selectionBody(request: SelectionRequest) {
  if (request.kind === "ids") return { ids: request.ids };
  const categoryId = request.filters.category_id;
  return {
    category_id: categoryId ? Number(categoryId) : undefined,
    excluded_ids: request.excluded,
    fresh: request.filters.fresh === "1" ? true : undefined,
    where: whereWithTitle(request.filters.where, request.filters.q),
  };
}

/** One library's works, as Radarr's three views: posters, overview, table (R1.6). Columns, sorts and filters come from
 *  the type's facets; an album wall is grouped by artist as Lidarr's, a poster type is tall as Sonarr's. */
export function WorkIndex() {
  const { categoryId, level } = useParams({ strict: false }) as { categoryId: string; level: string };
  useRecordLastLevel(categoryId, level);
  const library = useLibrary(categoryId);
  const info = useMediaTypes().find((entry) => entry.key === library?.media_type);
  const words = vocabularyOf(info);
  const levelLabel = library?.levels.find((entry) => entry.key === level)?.label ?? words.Works;
  const lead = info?.roles.find((role) => role.lead);
  const portrait = info?.work_image === "poster";

  const baseView = useRouteView(FILTER_KEYS);
  const routeView = useMemo<ViewState>(
    () => ({
      query: { ...baseView.query, filters: { ...baseView.query.filters, category_id: categoryId } },
      // The library is the route's, never a URL filter.
      setQuery: (next) => baseView.setQuery({ ...next, filters: Object.fromEntries(Object.entries(next.filters).filter(([key]) => key !== "category_id")) }),
    }),
    [baseView, categoryId],
  );
  const [storedView, setView] = usePreference<IndexView>(`works.view.${categoryId}`, "poster");
  const view: IndexView = storedView === "list" || storedView === "overview" ? storedView : "poster";
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const [hidden, setHidden] = usePreference<string[]>(`works.hidden-columns.${library?.media_type ?? ""}`, []);
  const frameRef = useRef<HTMLDivElement>(null);
  const narrow = useNarrow();
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const isPoster = view === "poster";
  const defaultSort = lead ? "artist" : "title";
  const sort = routeView.query.sort ?? defaultSort;
  // Albums as posters are grouped by artist (2026-09-17), which needs them in artist order.
  const grouped = isPoster && lead !== undefined && sort === "artist";
  const listView = useMemo<ViewState>(
    () => (grouped ? { ...routeView, query: { ...routeView.query, sort: "artist", direction: "asc" } } : routeView),
    [grouped, routeView],
  );
  const entryOf = (work: Work) => workEntry(work, categoryId, info);

  const list = useLibraryList<Work>(
    {
      // The library rides in the filters, so the cache key is per library while every page invalidates "works".
      name: "works",
      groupOf: grouped ? (work) => work.artist ?? "Unknown artist" : undefined,
      groupHeaderHeight: GROUP_HEADER_HEIGHT,
      rowHeight: isPoster
        ? posterRowHeight(options, metrics.cellWidth, portrait ? 1.5 : 1)
        : view === "overview" ? OVERVIEW_ROW_HEIGHT : narrow ? CARD_HEIGHT : TABLE_ROW_HEIGHT,
      itemsPerRow: isPoster ? metrics.columns : 1,
      idOf: (work) => work.id,
      fetchPage: async ({ cursor, query, signal }) => {
        const { data, error } = await api.GET("/works", {
          params: {
            query: {
              cursor, limit: 100, category_id: Number(categoryId),
              fresh: query.filters.fresh === "1" ? true : undefined,
              where: whereWithTitle(query.filters.where, query.filters.q),
              sort: query.sort ?? defaultSort,
              dir: query.direction === "desc" ? "desc" : undefined,
            },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    listView,
  );

  const columns = useMemo(() => buildColumns(info, categoryId, new Set(hidden)), [info, categoryId, hidden]);
  const choosable = useMemo(() => buildColumns(info, categoryId, new Set()).filter((column) => column.header !== ""), [info, categoryId]);
  const setQuery = (next: Partial<ViewState["query"]>) => routeView.setQuery({ ...routeView.query, ...next });
  const setFilter = (key: string, value: string) => setQuery({ filters: { ...routeView.query.filters, [key]: value } });

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <Toolbar title={levelLabel}>
        <Button
          variant={routeView.query.filters.fresh === "1" ? "primary" : "secondary"}
          aria-pressed={routeView.query.filters.fresh === "1"}
          title="Out within the last week"
          onClick={() => setFilter("fresh", routeView.query.filters.fresh === "1" ? "" : "1")}
        >
          Fresh
        </Button>
        <ToolbarSearch value={routeView.query.filters.q ?? ""} onChange={(value) => setFilter("q", value)} placeholder="Title contains…" label={`Filter ${words.works} by title`} />
        <CustomFilterMenu
          fields={filterFieldsOf(info)}
          storageKey={`${categoryId}.${level}`}
          where={routeView.query.filters.where ?? ""}
          onChange={(where) => setFilter("where", where)}
        />
        <ToolbarSort
          sort={sort}
          descending={routeView.query.direction === "desc"}
          choices={sortChoicesOf(info)}
          onSort={(next) => setQuery({ sort: next })}
          onTurn={() => setQuery({ sort, direction: routeView.query.direction === "desc" ? "asc" : "desc" })}
        />
        <Button onClick={list.selectAllMatching}>Select all matching</Button>
        {view === "list" && (
          <details className="relative">
            <summary className="cursor-pointer list-none rounded-md border border-border-subtle px-3 py-1.5 text-sm">Columns</summary>
            <div className="absolute right-0 top-full z-10 mt-1 flex flex-col gap-1 rounded-md border border-border-subtle bg-surface p-2 shadow-lg">
              {choosable.map((column) => (
                <label key={column.key} className="flex items-center gap-1.5 whitespace-nowrap px-1 py-0.5">
                  <input
                    type="checkbox"
                    checked={!hidden.includes(column.key)}
                    onChange={() => setHidden(hidden.includes(column.key) ? hidden.filter((key) => key !== column.key) : [...hidden, column.key])}
                  />
                  {column.header}
                </label>
              ))}
            </div>
          </details>
        )}
        {isPoster && <PosterOptionsMenu options={options} onChange={setOptions} />}
        <IndexViewToggle view={view} onChange={setView} />
        <ToolbarAdd categoryId={categoryId} />
      </Toolbar>
      {!list.selectionIsEmpty && list.selectionRequest && (
        <BulkBar
          request={list.selectionRequest}
          count={list.selectionCount}
          info={info}
          explicitIds={list.items.filter((work) => list.isSelected(work.id)).map((work) => work.id)}
          onDone={() => list.clearSelection()}
        />
      )}
      <Exclusions mediaType={library?.media_type} words={words} />
      <div className="min-h-0 flex-1">
        {list.isLoading && <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>}
        {list.error !== null && <p className="p-6 text-sm text-danger">The library could not be read.</p>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <div className="p-6">
            <EmptyState
              title={routeView.query.filters.where || routeView.query.filters.fresh || routeView.query.filters.q ? `No ${words.works} match these filters.` : `No ${words.works} in this library yet.`}
              hint={`Identified files appear here once a scan finds them; Add new looks ${words.works} up.`}
            />
          </div>
        )}
        {list.items.length > 0 &&
          (view === "list" ? (
            <ListTable list={list} columns={columns} idOf={(work) => work.id} />
          ) : view === "overview" ? (
            <OverviewList list={list} entryOf={entryOf} />
          ) : (
            <PosterGrid list={list} idOf={(work) => work.id} renderPoster={(work) => <IndexPoster entry={entryOf(work)} options={options} />} />
          ))}
      </div>
    </div>
  );
}

/** The table's columns: picture and title always, then the base fields and the type's column facets, less the hidden. */
function buildColumns(info: MediaTypeInfo | undefined, categoryId: string, hidden: ReadonlySet<string>): Column<Work>[] {
  const lead = info?.roles.find((role) => role.lead);
  const columns: Column<Work>[] = [
    {
      key: "cover",
      header: "",
      width: "3rem",
      // Sized to fit the 44 px row: a 2:3 poster 28 px wide, a square cover 36 px.
      render: (work) => (
        <div className={info?.work_image === "poster" ? "w-7" : "w-9"}>
          <Image
            shape={info?.work_image === "poster" ? "poster" : "cover"}
            title={work.title}
            src={workEntry(work, categoryId, info).imageSrc}
            artwork={work.poster_url || work.no_artwork ? undefined : { subject: "works", id: work.id, size: "thumb" }}
          />
        </div>
      ),
    },
    {
      key: "title",
      header: "Title",
      sortable: true,
      width: "minmax(14rem,3fr)",
      render: (work) => (
        <Link to="/library/$categoryId/works/$workId" params={{ categoryId, workId: String(work.id) }} onClick={(event) => event.stopPropagation()} className="hover:underline">
          {work.title}
          {work.unverified && <span className="ml-2 text-xs text-ink-muted">unverified</span>}
        </Link>
      ),
    },
  ];
  const optional: Column<Work>[] = [];
  if (lead) optional.push({ key: "artist", header: lead.label, sortable: true, render: (work) => work.artist ?? "" });
  optional.push({ key: "year", header: "Year", width: "5rem", sortable: true, render: (work) => work.year ?? "" });
  for (const facet of info?.facets ?? []) {
    if (!facet.column || facet.entity !== "work" || facet.key === "year") continue;
    optional.push({
      key: facet.sortable ? `facet:${facet.key}` : facet.key,
      header: facet.label,
      sortable: facet.sortable,
      width: "minmax(6rem,1fr)",
      render: (work) =>
        (work.facets?.[facet.key] ?? [])
          .map((value) => (facet.kind === "duration" ? formatMinutes(Number(value)) : (facet.choices?.find((choice) => choice.key === value)?.label ?? value)))
          .join(", "),
    });
  }
  optional.push(
    { key: "parts", header: info?.part.many || "Parts", width: "6rem", render: (work) => (work.totals ? `${work.totals.present}/${work.totals.required}` : "") },
    { key: "added", header: "Added", width: "8rem", sortable: true, render: (work) => new Date(work.added_at).toLocaleDateString() },
    { key: "size", header: "Size", width: "6rem", sortable: true, render: (work) => (work.size_bytes ? formatBytes(work.size_bytes) : "") },
    { key: "status", header: "Availability", width: "8rem", sortable: true, render: (work) => (work.status ? albumStatusLabel(work.status) : "") },
    { key: "monitored", header: "Monitored", width: "6rem", render: (work) => <RowActions column="monitored" entry={workEntry(work, categoryId, info)} /> },
    { key: "actions", header: "Actions", width: "7rem", render: (work) => <RowActions column="actions" entry={workEntry(work, categoryId, info)} /> },
  );
  return [...columns, ...optional.filter((column) => !hidden.has(column.key))];
}

/** Sonarr's and Radarr's editor for the selection: monitor, edit, and for chosen works a monitor option or removal. */
function BulkBar({
  request,
  count,
  info,
  explicitIds,
  onDone,
}: {
  request: SelectionRequest;
  count: number | null;
  info: MediaTypeInfo | undefined;
  explicitIds: number[];
  onDone: () => void;
}) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const [confirm, setConfirm] = useState<
    { kind: "monitor"; monitored: boolean; count: number } | { kind: "option"; option: string; count: number } | { kind: "remove" }
  >();
  const [editing, setEditing] = useState(false);
  const [option, setOption] = useState(info?.monitor_options[0]?.key ?? "");
  const [removal, setRemoval] = useState({ deleteFiles: false, exclude: true });
  const [progress, setProgress] = useState<string>();
  const changed = () => {
    setConfirm(undefined);
    onDone();
    void queryClient.invalidateQueries({ queryKey: ["works"] });
  };
  const preview = useMutation({
    mutationFn: async (monitored: boolean) => {
      const { data, error } = await api.POST("/works/bulk/monitor", { body: { ...selectionBody(request), monitored, preview: true } });
      if (error) throw error;
      return { monitored, count: data.count };
    },
    onSuccess: ({ monitored, count: matched }) => setConfirm({ kind: "monitor", monitored, count: matched }),
  });
  // A monitor option goes through the bulk edit, so "select all matching" reaches every work, counted first.
  const optionPreview = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/works/bulk/edit", { body: { ...selectionBody(request), monitor_option: option, preview: true } });
      if (error) throw error;
      return data.count;
    },
    onSuccess: (matched) => setConfirm({ kind: "option", option, count: matched }),
  });
  const apply = useMutation({
    mutationFn: async () => {
      if (confirm?.kind === "option") {
        const { error } = await api.POST("/works/bulk/edit", { body: { ...selectionBody(request), monitor_option: confirm.option } });
        if (error) throw error;
        return;
      }
      if (confirm?.kind === "monitor") {
        const { error } = await api.POST("/works/bulk/monitor", { body: { ...selectionBody(request), monitored: confirm.monitored } });
        if (error) throw error;
        return;
      }
      let failed = 0;
      for (const [index, id] of explicitIds.entries()) {
        setProgress(`${index + 1} of ${explicitIds.length}`);
        const { error } = await api.POST("/works/{id}/remove", { params: { path: { id } }, body: { delete_files: removal.deleteFiles, exclude: removal.exclude } });
        if (error) failed++;
      }
      setProgress(failed > 0 ? `${failed} could not be changed` : undefined);
    },
    onSuccess: changed,
  });
  const selected = count === null ? "All matching" : `${count} selected`;
  const optionLabel = info?.monitor_options.find((entry) => entry.key === option)?.label ?? option;
  const words = vocabularyOf(info);
  return (
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-border-subtle bg-surface px-4 py-2 text-xs">
      <span className="font-medium">{selected}</span>
      <Button busy={preview.isPending} onClick={() => preview.mutate(true)}>
        {t("action.monitor")}
      </Button>
      <Button busy={preview.isPending} onClick={() => preview.mutate(false)}>
        {t("action.unmonitor")}
      </Button>
      <Button onClick={() => setEditing(true)}>{t("action.edit")}</Button>
      {(info?.monitor_options.length ?? 0) > 0 && (
        <span className="flex items-center gap-1">
          <select value={option} onChange={(event) => setOption(event.target.value)} className="rounded-md border border-border-subtle bg-canvas px-2 py-1">
            {info?.monitor_options.map((entry) => (
              <option key={entry.key} value={entry.key}>
                {entry.label}
              </option>
            ))}
          </select>
          <Button busy={preview.isPending} onClick={() => optionPreview.mutate()}>
            Apply
          </Button>
        </span>
      )}
      {explicitIds.length > 0 && administrator && (
        <Button variant="danger" onClick={() => setConfirm({ kind: "remove" })}>
          {t("action.remove")}
        </Button>
      )}
      <Button variant="ghost" onClick={onDone}>
        Clear
      </Button>
      {progress && <span className="text-ink-muted">{progress}</span>}
      {editing && (
        <BulkEditWork
          selection={selectionBody(request)}
          info={info}
          onClose={() => setEditing(false)}
          onDone={() => {
            setEditing(false);
            changed();
            void queryClient.invalidateQueries({ queryKey: ["tags"] });
          }}
        />
      )}
      {confirm && (
        <ConfirmDialog
          title={confirm.kind === "remove" ? `${t("action.remove")} ${explicitIds.length} ${words.works}` : "Change the selection"}
          consequence={
            confirm.kind === "monitor"
              ? `${confirm.monitored ? "Monitor" : "Unmonitor"} ${confirm.count} ${confirm.count === 1 ? words.work : words.works}.`
              : confirm.kind === "option"
                ? `Monitor ${optionLabel.toLowerCase()} of ${confirm.count} ${confirm.count === 1 ? words.work : words.works}. ${words.Parts} already chosen by hand are decided again.`
                : t("work.remove.consequence", words)
          }
          confirmLabel={confirm.kind === "remove" ? t("action.remove") : "Apply"}
          danger={confirm.kind === "remove"}
          busy={apply.isPending}
          onCancel={() => setConfirm(undefined)}
          onConfirm={() => apply.mutate()}
        >
          {confirm.kind === "remove" && (
            <div className="flex flex-col gap-2 text-sm">
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={removal.deleteFiles} onChange={(event) => setRemoval({ ...removal, deleteFiles: event.target.checked })} />
                {t("work.remove.deleteFiles")}
              </label>
              <label className="flex items-center gap-2">
                <input type="checkbox" checked={removal.exclude} onChange={(event) => setRemoval({ ...removal, exclude: event.target.checked })} />
                Do not add them back
              </label>
            </div>
          )}
        </ConfirmDialog>
      )}
    </div>
  );
}

/** The works removed with "do not add it back", and the way to allow one again. Administrators only. */
function Exclusions({ mediaType, words }: { mediaType: string | undefined; words: ReturnType<typeof vocabularyOf> }) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const excluded = useQuery({
    queryKey: ["work-exclusions", mediaType],
    enabled: administrator && mediaType !== undefined,
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/exclusions", { params: { query: { media_type: mediaType ?? "" } } });
      if (error) throw error;
      return data;
    },
  });
  const allow = useMutation({
    mutationFn: async (entry: { provider: string; external_id: string }) => {
      const { error } = await api.DELETE("/works/exclusions", { params: { query: { media_type: mediaType ?? "", ...entry } } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["work-exclusions", mediaType] }),
  });
  if (!excluded.data || excluded.data.length === 0) return null;
  return (
    <details className="border-b border-border-subtle px-4 py-1.5 text-xs text-ink-muted">
      <summary className="cursor-pointer">
        Removed {words.works} that are not added back ({excluded.data.length})
      </summary>
      <ul className="mt-1 flex flex-col gap-1">
        {excluded.data.map((entry) => (
          <li key={`${entry.provider}:${entry.external_id}`} className="flex items-center gap-2">
            <span className="text-ink">{entry.title}</span>
            <Button variant="ghost" busy={allow.isPending} onClick={() => allow.mutate(entry)}>
              Allow again
            </Button>
          </li>
        ))}
      </ul>
    </details>
  );
}
