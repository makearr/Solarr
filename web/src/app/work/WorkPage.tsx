import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { FilePen, Filter, Layers, ListChecks, ListOrdered, Pencil, RefreshCw, Search, Trash2, UserRound } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { HistoryKind, historySource, historyWhen } from "@/lib/downloadHistory";
import { t, vocabularyOf, type Vocabulary } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useMediaTypes, type MediaTypeInfo } from "@/lib/useMediaTypes";
import { usePreference } from "@/lib/usePreference";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { AddToCollection } from "./AddToCollection";
import { ConfirmDialog } from "@/ui/ConfirmDialog";
import { Dialog } from "@/ui/Dialog";
import { EmptyState } from "@/ui/EmptyState";
import { Image } from "@/ui/Image";
import { PageHeader } from "@/ui/PageHeader";
import { Tabs, type Tab } from "@/ui/Tabs";

import { ChooseImage } from "../ChooseImage";
import { InteractiveSearch, type SearchScope } from "../InteractiveSearch";
import { RenamePlanner } from "../RenamePlanner";
import { statusLabel, useQueue } from "../Queue";
import { CrossoverBadges, CrossoverDialog, CrossoverGuestRows, useWorkCrossovers } from "../Crossovers";
import { useLibrary } from "@/lib/useLibrary";
import { EditWork } from "./EditWork";
import { NumberingDialog, useNumberingChoices } from "./NumberingDialog";
import { ReleaseFiltersDialog } from "./ReleaseFiltersDialog";
import { grabNote } from "./grabNote";
import { externalLinks, fileChips, formatMinutes, isUnverified } from "./workFormat";
import { GroupSection, MonitorToggle, PartTable, PersonLink, useAllParts, type PartActions } from "./WorkParts";
import { WorkCollections } from "./WorkCollections";
import { WorkSearchTab } from "./WorkSearchTab";

type Work = components["schemas"]["WorkDetail"];
type Group = components["schemas"]["WorkGroup"];
type Credit = components["schemas"]["Credit"];
type Edition = components["schemas"]["Edition"];

const TABS = ["overview", "files", "history", "search"] as const;
type TabKey = (typeof TABS)[number];

/** One work of any media type, laid out as its reference application lays it out: Sonarr's series with season
 *  accordions when the type groups its parts, Lidarr's album with one tracklist when it does not (R1.6). */
export function WorkPage() {
  const params = useParams({ strict: false }) as { categoryId?: string; workId?: string; id?: string; tab?: string };
  const id = Number(params.workId ?? params.id);
  const tab: TabKey = (TABS as readonly string[]).includes(params.tab ?? "") ? (params.tab as TabKey) : "overview";
  const detail = useQuery({
    queryKey: ["work", id],
    enabled: Number.isFinite(id),
    queryFn: () => ok(api.GET("/works/{id}", { params: { path: { id } } })),
  });
  const info = useMediaTypes().find((entry) => entry.key === detail.data?.media_type);
  if (detail.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>;
  if (!detail.data) return <p className="p-6 text-sm text-danger">{t("work.notFound", vocabularyOf(info))}</p>;
  const categoryId = params.categoryId ?? (detail.data.library_ids?.[0] !== undefined ? String(detail.data.library_ids[0]) : undefined);
  return <WorkView work={detail.data} info={info} categoryId={categoryId} tab={tab} />;
}

function WorkView({ work, info, categoryId, tab }: { work: Work; info: MediaTypeInfo | undefined; categoryId: string | undefined; tab: TabKey }) {
  const words = vocabularyOf(info);
  const tabLink = (key: TabKey): Tab["link"] =>
    categoryId
      ? { to: "/library/$categoryId/works/$workId/$tab", params: { categoryId, workId: String(work.id), tab: key } }
      : { to: "/works/$id/$tab", params: { id: String(work.id), tab: key } };
  const tabs: Tab[] = [
    { key: "overview", label: t("tab.overview"), link: tabLink("overview") },
    { key: "files", label: t("tab.files"), link: tabLink("files") },
    { key: "history", label: t("tab.history"), link: tabLink("history") },
    { key: "search", label: t("tab.search"), link: tabLink("search") },
  ];
  const lead = work.credits.find((credit) => credit.person_id === work.lead_id);
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-4 p-4">
        <WorkHeader work={work} info={info} words={words} categoryId={categoryId} lead={lead} interactive={tabLink("search")} />
        <MergeSuggestion work={work} words={words} categoryId={categoryId} />
        <Tabs label={work.title} tabs={tabs} active={tab} />
        {tab === "overview" && <OverviewTab work={work} info={info} words={words} categoryId={categoryId} />}
        {tab === "files" && <FilesTab work={work} words={words} info={info} />}
        {tab === "history" && <HistoryTab workId={work.id} />}
        {tab === "search" && <WorkSearchTab work={work} info={info} words={words} lead={lead?.person_name ?? ""} />}
      </div>
    </div>
  );
}

// An album held twice (enrichment found its release is another album's): an administrator previews and merges it.
function MergeSuggestion({ work, words, categoryId }: { work: Work; words: Vocabulary; categoryId: string | undefined }) {
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const suggestion = useQuery({
    queryKey: ["work-merge", work.id],
    enabled: administrator,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/works/{id}/merge-suggestion", { params: { path: { id: work.id } } });
      if (error) throw error;
      return response.status === 204 ? null : (data ?? null);
    },
  });
  const merge = useMutation({
    mutationFn: async (preview: boolean) => {
      const into = suggestion.data?.into_work_id ?? 0;
      return ok(api.POST("/works/{id}/merge", { params: { path: { id: work.id } }, body: { into, preview } }));
    },
    onSuccess: (result, preview) => {
      if (preview || !result) return;
      void queryClient.invalidateQueries({ queryKey: ["works"] });
      void navigate(
        categoryId
          ? { to: "/library/$categoryId/works/$workId/$tab", params: { categoryId, workId: String(result.keep_id), tab: "overview" } }
          : { to: "/works/$id/$tab", params: { id: String(result.keep_id), tab: "overview" } },
      );
    },
  });
  const other = suggestion.data;
  if (!other) return null;
  const otherName = other.lead_name ? `${other.title} (${other.lead_name})` : other.title;
  const otherLink = categoryId
    ? ({ to: "/library/$categoryId/works/$workId/$tab", params: { categoryId, workId: String(other.into_work_id), tab: "overview" } } as const)
    : ({ to: "/works/$id/$tab", params: { id: String(other.into_work_id), tab: "overview" } } as const);
  const preview = merge.variables === true ? merge.data : undefined;
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-md border border-warn/40 bg-warn/10 p-3 text-sm">
      <p>
        {t("work.merge.heldTwice", words)}{" "}
        <Link {...otherLink} className="font-medium underline">
          {otherName}
        </Link>{" "}
        {t("work.merge.sameRelease")}
      </p>
      {preview && (
        <p className="text-ink-muted">
          {t("work.merge.preview", { ...words, files: preview.files, downloads: preview.acquisitions, other: otherName })}
        </p>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {preview ? (
          <Button variant="danger" busy={merge.isPending} onClick={() => merge.mutate(false)}>
            {t("work.merge.confirm")}
          </Button>
        ) : (
          <Button busy={merge.isPending} onClick={() => merge.mutate(true)}>
            {t("work.merge.review")}
          </Button>
        )}
        {merge.isError && <span className="text-xs text-danger">{t("work.merge.failed")}</span>}
      </div>
    </div>
  );
}

function WorkHeader({
  work,
  info,
  words,
  categoryId,
  lead,
  interactive,
}: {
  work: Work;
  info: MediaTypeInfo | undefined;
  words: Vocabulary;
  categoryId: string | undefined;
  lead: Credit | undefined;
  interactive: Tab["link"];
}) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const administrator = useIsAdministrator();
  const changed = () => void queryClient.invalidateQueries({ queryKey: ["work", work.id] });
  const groups = work.groups ?? [];
  const monitored = groups.some((group) => group.monitored);
  const [searchOnMonitor] = usePreference("discover.search_on_add", true);
  const [editing, setEditing] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [collecting, setCollecting] = useState(false);
  const library = useLibrary(categoryId ?? "");
  const [numbering, setNumbering] = useState(false);
  const [filtering, setFiltering] = useState(false);
  const numberings = useNumberingChoices(work.id, administrator);
  const [removal, setRemoval] = useState({ deleteFiles: false, exclude: true });
  const [note, setNote] = useState<string>();

  const monitor = useMutation({
    mutationFn: async (next: boolean) => {
      await ok(api.POST("/works/{id}/monitor", { params: { path: { id: work.id } }, body: { monitored: next, search: next && searchOnMonitor } }));
    },
    onSuccess: changed,
  });
  // A series is searched season by season on the server: Sonarr's "Search monitored".
  const search = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id: work.id } } });
      if (error) throw error;
      return grabNote(data);
    },
    onSuccess: (summary) => {
      setNote(summary);
      changed();
    },
    onError: () => setNote(t("search.failed")),
  });
  const refresh = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/works/{id}/refresh", { params: { path: { id: work.id } } }));
    },
    onSuccess: changed,
  });
  const remove = useMutation({
    mutationFn: () => ok(api.POST("/works/{id}/remove", { params: { path: { id: work.id } }, body: { delete_files: removal.deleteFiles, exclude: removal.exclude } })),
    onSuccess: (result) => {
      setRemoving(false);
      void queryClient.invalidateQueries({ queryKey: ["works"] });
      if (result?.work_deleted) void navigate(categoryId ? { to: "/library/$categoryId/overview", params: { categoryId } } : { to: "/" });
      else changed();
    },
  });

  const poster = work.pictures?.poster;
  const shape = info?.work_image ?? "cover";
  // A file of a part that is not wanted is still held: it counts on both sides, never as more than there is.
  const counts = groups.reduce(
    (sum, group) => ({ present: sum.present + group.present_count, required: sum.required + Math.max(group.required_count ?? 0, group.present_count) }),
    { present: 0, required: 0 },
  );
  const facts = headerFacts(work, info);
  const genres = work.facets?.genre ?? work.genres;
  const links = externalLinks(work.external_ids);

  return (
    <>
      <PageHeader
        image={
          <div className="flex flex-col items-center gap-2">
            <Image shape={shape} title={work.title} src={poster ? remoteImage(poster, "small") : undefined} artwork={poster ? undefined : { subject: "works", id: work.id, size: "full" }} />
            <ChooseImage subject="works" id={work.id} title={work.title} shape={shape} onChosen={changed} />
          </div>
        }
        title={work.pictures?.logo ? <img src={remoteImage(work.pictures.logo, "small")} alt={work.title} className="max-h-20 max-w-xs object-contain object-left" /> : work.title}
        subtitle={
          <span className="flex flex-wrap items-center gap-x-2">
            {lead && (
              <>
                <PersonLink id={lead.person_id} name={lead.person_name} categoryId={categoryId} />
                {facts.length > 0 && "·"}
              </>
            )}
            {facts.map((fact, index) => {
              const organisation = work.organisations?.find((entry) => entry.name === fact);
              const link = organisation
                ? categoryId
                  ? ({ to: "/library/$categoryId/organisations/$organisationId", params: { categoryId, organisationId: String(organisation.id) } } as const)
                  : ({ to: "/organisations/$id", params: { id: String(organisation.id) } } as const)
                : undefined;
              return (
                <span key={`${fact}-${index}`}>
                  {index > 0 && " · "}
                  {link ? (
                    <Link {...link} className="hover:text-ink hover:underline">
                      {fact}
                    </Link>
                  ) : (
                    fact
                  )}
                </span>
              );
            })}
          </span>
        }
        facts={
          <div className="flex flex-col gap-2">
            <div className="flex flex-wrap items-center gap-2">
              {isUnverified(work.external_ids) && (
                <Chip tone="warn" title="Named from its own tags or folder: no metadata source knew it, so it is not searched for.">
                  Unverified
                </Chip>
              )}
              {info?.part.one && groups.length > 0 && work.part_count === 0 && <Chip>{t("work.waitingForMetadata")}</Chip>}
              {groups.length > 0 && work.part_count > 0 && (
                <Chip tone={counts.required > 0 && counts.present >= counts.required ? "ok" : "neutral"}>
                  {counts.required > 0
                    ? t("work.partsOnDisk", { ...words, present: counts.present, required: counts.required })
                    : t("work.partsPresent", { ...words, present: counts.present })}
                </Chip>
              )}
              {work.rating && (
                <Chip tone={work.rating.above_limit ? "warn" : "neutral"} title={work.rating.age < 0 ? "A rating Solarr cannot read counts as unrated." : `Admits ${work.rating.age} and older.`}>
                  Rated {work.rating.text || "unrated"}
                </Chip>
              )}
              {work.rating?.above_limit && (
                <Chip tone="warn" title="Its library's maximum rating does not admit it: it stays, but is never searched there. Move it to another library or raise the limit.">
                  Above this library's rating
                </Chip>
              )}
              <DownloadingBadge workId={work.id} />
            </div>
            {work.description && <p className="max-w-3xl text-sm text-ink-muted">{work.description}</p>}
            <PeopleLine work={work} info={info} categoryId={categoryId} />
            {genres.length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {genres.map((genre) => (
                  <Chip key={genre}>{genre}</Chip>
                ))}
              </div>
            )}
            {links.length > 0 && (
              <p className="flex flex-wrap gap-3 text-xs">
                {links.map((link) => (
                  <a key={link.label} href={link.href} target="_blank" rel="noreferrer" className="text-accent hover:underline">
                    {link.label}
                  </a>
                ))}
              </p>
            )}
          </div>
        }
        actions={
          <>
            <Button
              disabled={!work.can_request}
              busy={monitor.isPending}
              title={work.can_request ? undefined : "Monitoring needs request access to this library"}
              icon={<MonitorIcon monitored={monitored} />}
              onClick={() => monitor.mutate(!monitored)}
            >
              {monitored ? "Monitored" : t("action.monitor")}
            </Button>
            {work.can_request && groups.length > 0 && (
              <Button busy={search.isPending} busyLabel="Searching…" icon={<Search aria-hidden className="size-4" />} onClick={() => search.mutate()}>
                {t("action.search")}
              </Button>
            )}
            {work.can_request && groups.length > 0 && (
              <Button icon={<ListChecks aria-hidden className="size-4" />} onClick={() => void navigate(interactive)}>
                {t("action.interactive")}
              </Button>
            )}
            {work.can_request && (
              <Button busy={refresh.isPending} icon={<RefreshCw aria-hidden className={`size-4 ${refresh.isPending ? "animate-spin" : ""}`} />} onClick={() => refresh.mutate()}>
                {t("action.refresh")}
              </Button>
            )}
            {work.can_request && (
              <Button icon={<Pencil aria-hidden className="size-4" />} onClick={() => setEditing(true)}>
                {t("action.edit")}
              </Button>
            )}
            {administrator && categoryId !== undefined && library?.levels.some((level) => level.entity === "franchise") && (
              <Button icon={<Layers aria-hidden className="size-4" />} onClick={() => setCollecting(true)}>
                {t("collection.add.button")}
              </Button>
            )}
            {administrator && categoryId !== undefined && (
              <Button icon={<FilePen aria-hidden className="size-4" />} onClick={() => setRenaming(true)}>
                {t("action.renamePreview")}
              </Button>
            )}
            {administrator && numberings.data && (
              <Button icon={<ListOrdered aria-hidden className="size-4" />} onClick={() => setNumbering(true)}>
                {t("numbering.title")}
              </Button>
            )}
            {administrator && info?.release_filters && (
              <Button icon={<Filter aria-hidden className="size-4" />} onClick={() => setFiltering(true)}>
                {t("releaseFilters.title")}
              </Button>
            )}
            {administrator && (
              <Button variant="danger" icon={<Trash2 aria-hidden className="size-4" />} onClick={() => setRemoving(true)}>
                {t("action.remove")}
              </Button>
            )}
          </>
        }
      />
      {(note || refresh.isError || monitor.isError) && (
        <p className="text-xs text-ink-muted">{refresh.isError ? "Refresh failed." : monitor.isError ? "That could not be changed." : note}</p>
      )}
      {editing && <EditWork work={work} info={info} onClose={() => setEditing(false)} />}
      {filtering && <ReleaseFiltersDialog workId={work.id} title={work.title} onClose={() => setFiltering(false)} />}
      {numbering && <NumberingDialog workId={work.id} title={work.title} onClose={() => setNumbering(false)} />}
      {renaming && categoryId !== undefined && (
        <Dialog title={t("action.renamePreview")} description={work.title} width="wide" onClose={() => setRenaming(false)}>
          <RenamePlanner categoryId={Number(categoryId)} workId={work.id} previewAtOnce />
        </Dialog>
      )}
      {collecting && categoryId !== undefined && <AddToCollection categoryId={categoryId} workId={work.id} title={work.title} onClose={() => setCollecting(false)} />}
      {removing && (
        <ConfirmDialog
          title={t("work.remove.title", { title: work.title })}
          consequence={t("work.remove.consequence", words)}
          confirmLabel={t("action.remove")}
          danger
          busy={remove.isPending}
          onCancel={() => setRemoving(false)}
          onConfirm={() => remove.mutate()}
        >
          <div className="flex flex-col gap-2 text-sm">
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={removal.deleteFiles} onChange={(event) => setRemoval({ ...removal, deleteFiles: event.target.checked })} />
              {t("work.remove.deleteFiles")}
            </label>
            <label className="flex items-center gap-2">
              <input type="checkbox" checked={removal.exclude} onChange={(event) => setRemoval({ ...removal, exclude: event.target.checked })} />
              Do not add it back: follows and import lists leave it out
            </label>
            {remove.isError && <p className="text-xs text-danger">That could not be removed.</p>}
          </div>
        </ConfirmDialog>
      )}
    </>
  );
}

function MonitorIcon({ monitored }: { monitored: boolean }) {
  return monitored ? <span aria-hidden className="size-2 rounded-full bg-accent" /> : <span aria-hidden className="size-2 rounded-full border border-ink-muted" />;
}

/** The header's facts: year, then the type's own facets in its order (choices by their labels), genres apart. */
function headerFacts(work: Work, info: MediaTypeInfo | undefined): string[] {
  const facts: string[] = [];
  for (const facet of info?.facets ?? []) {
    if (facet.key === "genre" || facet.entity !== "work") continue;
    const values = work.facets?.[facet.key] ?? [];
    for (const value of values) {
      if (!value || value === "0") continue;
      if (facet.kind === "duration") facts.push(formatMinutes(Number(value)));
      else facts.push(facet.choices?.find((choice) => choice.key === value)?.label ?? value);
    }
  }
  if (facts.length === 0 && work.year) facts.push(String(work.year));
  return facts;
}

/** Who made it, one line per role the type declares for a whole work, the lead and the cast strip's actors aside. */
function PeopleLine({ work, info, categoryId }: { work: Work; info: MediaTypeInfo | undefined; categoryId: string | undefined }) {
  const roles = (info?.roles ?? []).filter((role) => !role.part_scoped && (!role.lead || !work.lead_id || info?.work_image === "poster") && !role.character);
  const lines = roles
    .map((role) => ({ role, people: work.credits.filter((credit, index, all) => credit.role === role.key && all.findIndex((other) => other.role === role.key && other.person_id === credit.person_id) === index) }))
    .filter((line) => line.people.length > 0);
  if (lines.length === 0) return null;
  return (
    <div className="flex max-w-3xl flex-col gap-0.5 text-xs text-ink-muted">
      {lines.map(({ role, people }) => (
        <p key={role.key}>
          <span className="font-medium text-ink">{role.label}</span>{" "}
          {people.slice(0, 6).map((credit, index) => (
            <span key={credit.person_id}>
              {index > 0 && ", "}
              <PersonLink id={credit.person_id} name={credit.person_name} categoryId={categoryId} />
            </span>
          ))}
        </p>
      ))}
    </div>
  );
}

function OverviewTab({ work, info, words, categoryId }: { work: Work; info: MediaTypeInfo | undefined; words: Vocabulary; categoryId: string | undefined }) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const changed = () => void queryClient.invalidateQueries({ queryKey: ["work", work.id] });
  const [interactive, setInteractive] = useState<{ scope: SearchScope; title: string }>();
  const [notes, setNotes] = useState<Record<string, string>>({});
  const groups = [...(work.groups ?? [])].reverse();
  const grouped = groups.some((group) => group.ref !== "");

  const monitor = useMutation({
    mutationFn: async (body: { monitored: boolean; group?: string; parts?: number[] }) => {
      await ok(api.POST("/works/{id}/monitor", { params: { path: { id: work.id } }, body }));
    },
    onSuccess: changed,
  });
  const grab = useMutation({
    mutationFn: async (scope: { group: Group; part?: number }) => {
      const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id: work.id }, query: { group: scope.group.ref, ...(scope.part ? { part: scope.part } : {}) } } });
      if (error) throw error;
      return { ref: scope.group.ref, note: grabNote(data) };
    },
    onSuccess: ({ ref, note }) => {
      setNotes((current) => ({ ...current, [ref]: note }));
      changed();
    },
    onError: (_error, scope) => setNotes((current) => ({ ...current, [scope.group.ref]: t("search.failed") })),
  });
  const removePart = useMutation({
    mutationFn: async ({ partId, deleteFiles }: { partId: number; deleteFiles: boolean }) => {
      await ok(api.POST("/works/{id}/parts/{partId}/remove", { params: { path: { id: work.id, partId } }, body: { delete_files: deleteFiles } }));
    },
    onSuccess: changed,
  });

  const library = useLibrary(categoryId ?? "");
  const hasCrossovers = library?.levels.some((level) => level.entity === "collection") === true;
  const crossovers = useWorkCrossovers(work.id, hasCrossovers);
  const [crossoverPart, setCrossoverPart] = useState<{ id: number; title?: string; media_ref: string }>();
  const actions: PartActions = {
    rowsAround: crossovers.data
      ? (part, placement) => (
          <CrossoverGuestRows partId={part.id} workId={work.id} placement={placement} events={crossovers.data.events} categoryId={categoryId} />
        )
      : undefined,
    badgesOf: crossovers.data
      ? (part) => <CrossoverBadges partId={part.id} events={crossovers.data.events} links={crossovers.data.same_content} categoryId={categoryId} />
      : undefined,
    onCrossover: hasCrossovers && administrator && categoryId ? (part) => setCrossoverPart(part) : undefined,
    onMonitor: work.can_request ? (part, monitored) => monitor.mutate({ monitored, parts: [part.id] }) : undefined,
    onGrab: grouped && work.can_request ? (part, group) => grab.mutate({ group, part: part.id }) : undefined,
    onInteractive: grouped && work.can_request ? (part, group) => setInteractive({ scope: { group: group.ref, part: part.id }, title: `${work.title}, ${group.label} ${part.number ?? ""}` }) : undefined,
    onRemove: administrator ? (part, deleteFiles) => removePart.mutate({ partId: part.id, deleteFiles }) : undefined,
  };
  const newest = groups.find((group) => group.ref !== "" && !group.label.toLowerCase().includes("special")) ?? groups[0];

  return (
    <div className="flex flex-col gap-4">
      <CastStrip work={work} info={info} categoryId={categoryId} />
      {categoryId && <WorkCollections workId={work.id} categoryId={categoryId} />}
      {crossoverPart && categoryId && <CrossoverDialog part={crossoverPart} categoryId={categoryId} onClose={() => setCrossoverPart(undefined)} />}
      {grouped ? (
        groups.map((group) => (
          <GroupSection
            key={group.ref}
            workId={work.id}
            group={group}
            initiallyOpen={group === newest}
            categoryId={categoryId}
            info={info}
            words={words}
            actions={actions}
            onGroupMonitor={work.can_request ? (monitored) => monitor.mutate({ monitored, group: group.ref }) : undefined}
            onGroupGrab={work.can_request ? () => grab.mutate({ group }) : undefined}
            onGroupInteractive={work.can_request ? () => setInteractive({ scope: { group: group.ref }, title: `${work.title}, ${group.label}` }) : undefined}
            note={grab.isPending && grab.variables.group.ref === group.ref && !grab.variables.part ? "Searching…" : notes[group.ref]}
          />
        ))
      ) : info?.part.one ? (
        <WholeParts work={work} info={info} words={words} categoryId={categoryId} actions={actions} />
      ) : (
        <WholeFiles work={work} />
      )}
      {work.editions.length > 0 && (
        <details className="rounded-lg border border-border-subtle bg-surface p-3 text-sm">
          <summary className="cursor-pointer text-xs font-medium uppercase tracking-wide text-ink-muted">Releases ({work.editions.length})</summary>
          <ul className="mt-2 flex flex-col gap-2">
            {work.editions.map((edition) => (
              <EditionCard key={edition.id} edition={edition} workTitle={work.title} />
            ))}
          </ul>
        </details>
      )}
      {interactive && <InteractiveSearch workId={work.id} scope={interactive.scope} title={interactive.title} onClose={() => setInteractive(undefined)} />}
    </div>
  );
}

/** Lidarr's tracklist: every part of an ungrouped work in one table. */
function WholeParts({ work, info, words, categoryId, actions }: { work: Work; info: MediaTypeInfo | undefined; words: Vocabulary; categoryId: string | undefined; actions: PartActions }) {
  const { parts, loading } = useAllParts(work.id, undefined);
  const group = work.groups?.find((entry) => entry.ref === "");
  if (loading) return <p className="text-xs text-ink-muted">{t("work.loading")}</p>;
  return (
    <section className="rounded-lg border border-border-subtle bg-surface">
      <header className="flex items-center gap-3 border-b border-border-subtle px-3 py-2">
        {group && <MonitorToggle monitored={group.monitored} label={work.title} />}
        <h2 className="text-sm font-semibold">{words.Parts}</h2>
        <span className="text-xs text-ink-muted">
          {parts.filter((part) => part.file).length}/{parts.length}
        </span>
      </header>
      <PartTable parts={parts} group={undefined} categoryId={categoryId} info={info} words={words} actions={{ ...actions, onGrab: undefined, onInteractive: undefined }} />
    </section>
  );
}

/** The order a work's people are shown in: actors, then who directed and wrote it, producers last. */
const CREW_ORDER = ["director", "creator", "showrunner", "writer", "composer", "cinematographer", "executive_producer", "producer"];

/** The work's people with their pictures: the actors first, then the director, the writers and the producers last; a person
 *  once, with every part they played. */
function CastStrip({ work, info, categoryId }: { work: Work; info: MediaTypeInfo | undefined; categoryId: string | undefined }) {
  const roles = (info?.roles ?? []).filter((role) => !role.part_scoped && (!role.lead || !work.lead_id));
  const labelOf = (key: string) => roles.find((role) => role.key === key)?.label ?? key;
  const actorRoles = new Set(roles.filter((role) => role.character).map((role) => role.key));
  const rank = (role: string) => {
    const index = CREW_ORDER.indexOf(role);
    return index < 0 ? CREW_ORDER.length : index;
  };
  const ordered = [
    ...work.credits.filter((credit) => actorRoles.has(credit.role)),
    ...work.credits.filter((credit) => !actorRoles.has(credit.role) && roles.some((role) => role.key === credit.role)).sort((a, b) => rank(a.role) - rank(b.role)),
  ];
  if (ordered.length === 0) return null;
  const people: { credit: Credit; subtitle: string[] }[] = [];
  for (const credit of ordered) {
    const found = people.find((person) => person.credit.person_id === credit.person_id);
    const part = actorRoles.has(credit.role) ? credit.character ?? labelOf(credit.role) : labelOf(credit.role);
    if (!found) people.push({ credit, subtitle: [part] });
    else if (part && !found.subtitle.includes(part)) found.subtitle.push(part);
  }
  const tile = (credit: Credit, subtitle: string) => {
    const link = categoryId
      ? ({ to: "/library/$categoryId/people/$personId", params: { categoryId, personId: String(credit.person_id) } } as const)
      : ({ to: "/people/$id", params: { id: String(credit.person_id) } } as const);
    return (
      <Link key={credit.person_id} {...link} className="flex w-24 shrink-0 flex-col items-center gap-1 text-center focus-visible:outline-2 focus-visible:outline-accent">
        <span className="block size-20 overflow-hidden rounded-full border border-border-subtle bg-surface">
          {credit.image_url ? (
            <img src={remoteImage(credit.image_url, "thumb")} alt="" loading="lazy" className="size-full object-cover" />
          ) : (
            <span className="flex size-full items-center justify-center text-ink-muted">
              <UserRound aria-hidden className="size-8" />
            </span>
          )}
        </span>
        <span className="w-full truncate text-xs font-medium">{credit.person_name}</span>
        <span className="w-full truncate text-[11px] text-ink-muted" title={subtitle}>
          {subtitle}
        </span>
      </Link>
    );
  };
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold">Cast and crew</h2>
      <div className="flex gap-3 overflow-x-auto pb-1">{people.slice(0, 40).map((person) => tile(person.credit, person.subtitle.join(" · ")))}</div>
    </section>
  );
}

function EditionCard({ edition, workTitle }: { edition: Edition; workTitle: string }) {
  // A label that is only the provider's id is an identity key, not a name (found 2026-09-28).
  const labelIsID = edition.external_ids.some((id) => id.external_id === edition.label);
  const fields = [edition.format, edition.catalogue_number, edition.barcode, edition.year ? String(edition.year) : undefined].filter((field): field is string => Boolean(field));
  return (
    <li className="flex flex-col gap-1 rounded-md border border-border-subtle p-2">
      <span className="flex items-center gap-2">
        <span className="font-medium">{labelIsID ? workTitle : edition.label}</span>
        <span className="text-xs text-ink-muted">{edition.kind}</span>
        {edition.is_default && <Chip tone="accent">default</Chip>}
      </span>
      {fields.length > 0 && <span className="text-xs text-ink-muted">{fields.join(" · ")}</span>}
    </li>
  );
}

/** Sonarr's Files tab: every part with a file, where it is, what the probe measured, and whether it meets the library's
 *  quality. */
function FilesTab({ work, words, info }: { work: Work; words: Vocabulary; info: MediaTypeInfo | undefined }) {
  const { parts, loading } = useAllParts(work.id, undefined);
  const present = parts.filter((part) => part.file);
  if (!info?.part.one) return <WholeFiles work={work} />;
  if (loading) return <p className="text-xs text-ink-muted">{t("work.loading")}</p>;
  if (present.length === 0) return <EmptyState title={`No ${words.parts} on disk yet.`} />;
  return (
    <table className="w-full text-sm">
      <tbody>
        {present.map((part) => (
          <tr key={part.id} className="border-b border-border-subtle last:border-0 align-top">
            <td className="w-24 py-1.5 pr-3 tabular-nums text-ink-muted">
              {part.section ? `${part.section} · ` : ""}
              {part.number}
            </td>
            <td className="py-1.5">
              <span>{part.title ?? part.media_ref}</span>
              {part.file?.path && <span className="block break-all font-mono text-xs text-ink-muted">{part.file.path}</span>}
              <span className="mt-0.5 flex flex-wrap gap-1">
                {part.file && fileChips(part.file).map((chip) => <Chip key={chip}>{chip}</Chip>)}
              </span>
            </td>
            <td className="whitespace-nowrap py-1.5 text-right text-xs">
              {part.file?.meets_target != null && <Chip tone={part.file.meets_target ? "ok" : "warn"}>target</Chip>}{" "}
              {part.file?.meets_cutoff != null && <Chip tone={part.file.meets_cutoff ? "ok" : "warn"}>cutoff</Chip>}
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** The files of a work with no parts (a movie): where each is and what the probe measured, a version each. */
function WholeFiles({ work }: { work: Work }) {
  const files = work.files ?? [];
  if (files.length === 0) {
    return <EmptyState title={work.wanted?.monitored ? t("work.file.missing") : t("work.file.none")} />;
  }
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <h2 className="text-sm font-semibold">{files.length > 1 ? t("work.file.versions", { count: files.length }) : t("work.file.one")}</h2>
      <ul className="flex flex-col divide-y divide-border-subtle text-sm">
        {files.map((file) => (
          <li key={file.media_object_id} className="flex flex-col gap-1 py-2">
            {file.path && <span className="break-all font-mono text-xs">{file.path}</span>}
            <span className="flex flex-wrap items-center gap-1">
              {fileChips(file).map((chip) => (
                <Chip key={chip}>{chip}</Chip>
              ))}
              {file.meets_target != null && <Chip tone={file.meets_target ? "ok" : "warn"}>{file.meets_target ? "meets the target" : "below the target"}</Chip>}
              {file.meets_cutoff === false && <Chip tone="warn">below the cutoff</Chip>}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

/** The work's own download history, as Lidarr's and Sonarr's History tabs show it. */
function HistoryTab({ workId }: { workId: number }) {
  const history = useQuery({
    queryKey: ["history", "work", workId],
    queryFn: async () => {
      const { data, error } = await api.GET("/history", { params: { query: { work_id: workId, limit: 50 } } });
      if (error) throw error;
      return data.items;
    },
  });
  if (history.isLoading) return <p className="text-xs text-ink-muted">{t("work.loading")}</p>;
  if (!history.data || history.data.length === 0) return <EmptyState title="Nothing has been downloaded for it yet." />;
  return (
    <ul className="flex flex-col gap-1 text-sm">
      {history.data.map((entry) => (
        <li key={entry.id} className="flex items-baseline gap-3">
          <time className="shrink-0 font-mono text-xs text-ink-muted" dateTime={entry.at}>
            {historyWhen(entry)}
          </time>
          <span className="w-16 shrink-0">
            <HistoryKind kind={entry.kind} />
          </span>
          <span className="min-w-0 truncate" title={entry.release_title}>
            {entry.release_title}
            {entry.detail && <span className={entry.kind === "failed" ? "text-danger" : "text-ink-muted"}> — {entry.detail}</span>}
          </span>
          <span className="ml-auto shrink-0 text-xs text-ink-muted">{historySource(entry)}</span>
        </li>
      ))}
    </ul>
  );
}

/** "Downloading 42%" while a release for this work is in a client, as Lidarr's and Sonarr's pages show it. */
function DownloadingBadge({ workId }: { workId: number }) {
  const queue = useQueue(workId);
  const item = queue.data?.items[0];
  if (!item) {
    return queue.data?.held.length ? (
      <Link to="/activity/queue" className="text-xs text-ink-muted hover:underline">
        Waiting for room in the download client
      </Link>
    ) : null;
  }
  const done = item.size_bytes ? Math.round((1 - (item.size_left_bytes ?? 0) / item.size_bytes) * 100) : undefined;
  return (
    <Link to="/activity/queue" title={item.release_title} className="text-xs text-accent hover:underline">
      {statusLabel(item)}
      {item.state !== "IMPORTING" && done !== undefined ? ` ${done}%` : ""}
    </Link>
  );
}

