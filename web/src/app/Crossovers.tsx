import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link2, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { Dialog } from "@/ui/Dialog";
import { EmptyState } from "@/ui/EmptyState";
import { PageHeader } from "@/ui/PageHeader";
import { inputStyle } from "@/ui/form";

type Crossover = components["schemas"]["Crossover"];
type Member = Crossover["members"][number];
type Suggestion = components["schemas"]["SameContentSuggestion"];
type SameContent = components["schemas"]["SameContentLink"];

/** "S01E05" for a part ref "s01:e05"; anything else as it is. */
export function refLabel(ref: string): string {
  const match = /^s(\d+):e(\d+)$/.exec(ref);
  return match ? `S${match[1]!.padStart(2, "0")}E${match[2]!.padStart(2, "0")}` : ref;
}

function seriesLink(member: { work_id: number }, categoryId?: string) {
  return categoryId
    ? ({ to: "/library/$categoryId/works/$workId/$tab", params: { categoryId, workId: String(member.work_id), tab: "overview" } } as const)
    : ({ to: "/works/$id/$tab", params: { id: String(member.work_id), tab: "overview" } } as const);
}

/** A library's crossover events, and episodes that look like one broadcast listed under two series (T23). */
export function CrossoverIndex() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const list = useQuery({
    queryKey: ["crossovers", categoryId],
    queryFn: () => ok(api.GET("/crossovers", { params: { query: { category_id: Number(categoryId) } } })),
  });
  const link = useMutation({
    mutationFn: async (suggestion: Suggestion) => {
      await ok(api.POST("/parts/{id}/same-content", { params: { path: { id: suggestion.part_id } }, body: { part_id: suggestion.other_part_id } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["crossovers", categoryId] }),
  });
  if (list.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>;
  if (!list.data) return <p className="p-6 text-sm text-danger">{t("crossover.failed")}</p>;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-4xl flex-col gap-5 p-4">
        <PageHeader title={t("crossover.title")} subtitle={t("crossover.help")} />
        {list.data.events.length === 0 && <EmptyState title={t("crossover.none")} />}
        {list.data.events.map((event) => (
          <section key={event.id} className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
            <Link to="/crossovers/$id" params={{ id: String(event.id) }} className="font-medium hover:underline">
              {event.name}
            </Link>
            <MemberList members={event.members} categoryId={categoryId} />
          </section>
        ))}
        {list.data.suggestions.length > 0 && (
          <section className="flex flex-col gap-2">
            <h2 className="text-sm font-semibold">{t("crossover.suggestions")}</h2>
            <p className="text-xs text-ink-muted">{t("crossover.suggestionsHelp")}</p>
            <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface text-sm">
              {list.data.suggestions.map((suggestion) => (
                <li key={`${suggestion.part_id}-${suggestion.other_part_id}`} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
                  <span className="font-medium">{suggestion.title}</span>
                  <span className="text-xs text-ink-muted">{suggestion.date}</span>
                  <span className="text-xs">
                    <Link {...seriesLink(suggestion, categoryId)} className="hover:underline">{suggestion.work_title}</Link> {refLabel(suggestion.media_ref)}
                    {" = "}
                    <Link {...seriesLink({ work_id: suggestion.other_work_id }, categoryId)} className="hover:underline">{suggestion.other_work_title}</Link>{" "}
                    {refLabel(suggestion.other_ref)}
                  </span>
                  {administrator && (
                    <Button className="ml-auto" busy={link.isPending && link.variables === suggestion} onClick={() => link.mutate(suggestion)}>
                      {t("crossover.sameBroadcast")}
                    </Button>
                  )}
                </li>
              ))}
            </ul>
          </section>
        )}
      </div>
    </div>
  );
}

function MemberList({ members, categoryId, onRemove }: { members: readonly Member[]; categoryId?: string; onRemove?: (member: Member) => void }) {
  return (
    <ol className="flex flex-col divide-y divide-border-subtle text-sm">
      {members.map((member, index) => (
        <li key={member.part_id} className="flex flex-wrap items-center gap-x-3 gap-y-1 py-1.5">
          <span className="w-6 text-right tabular-nums text-ink-muted">{index + 1}.</span>
          <Link {...seriesLink(member, categoryId)} className="hover:underline">{member.work_title}</Link>
          <span className="font-mono text-xs">{refLabel(member.media_ref)}</span>
          <span>{member.title}</span>
          <span className="text-xs text-ink-muted">{member.date}</span>
          <span className={`ml-auto text-xs ${member.has_file ? "text-ok" : member.monitored ? "text-danger" : "text-ink-muted"}`}>
            {member.has_file ? t("crossover.onDisk") : member.monitored ? t("crossover.missing") : t("crossover.unmonitored")}
          </span>
          {onRemove && (
            <Button variant="icon" aria-label={t("crossover.removeMember")} title={t("crossover.removeMember")} onClick={() => onRemove(member)}>
              <Trash2 aria-hidden className="size-3.5" />
            </Button>
          )}
        </li>
      ))}
    </ol>
  );
}

/** One crossover event in watch order, monitored as a whole if wanted. */
export function CrossoverPage() {
  const { id } = useParams({ strict: false }) as { id: string };
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const event = useQuery({
    queryKey: ["crossover", id],
    queryFn: () => ok(api.GET("/crossovers/{id}", { params: { path: { id: Number(id) } } })),
  });
  const changed = (data?: Crossover) => {
    if (data) queryClient.setQueryData(["crossover", id], data);
    void queryClient.invalidateQueries({ queryKey: ["crossovers"] });
    void queryClient.invalidateQueries({ queryKey: ["work-crossovers"] });
  };
  const monitor = useMutation({
    mutationFn: (monitored: boolean) => ok(api.POST("/crossovers/{id}/monitor", { params: { path: { id: Number(id) } }, body: { monitored } })),
    onSuccess: changed,
  });
  const update = useMutation({
    mutationFn: (members: readonly Member[]) => ok(api.PUT("/crossovers/{id}", {
      params: { path: { id: Number(id) } },
      body: { name: event.data!.name, part_ids: members.map((member) => member.part_id) },
    })),
    onSuccess: changed,
  });
  const remove = useMutation({
    mutationFn: async () => {
      await ok(api.DELETE("/crossovers/{id}", { params: { path: { id: Number(id) } } }));
    },
    onSuccess: () => {
      changed();
      void navigate({ to: "/" });
    },
  });
  if (event.isLoading) return <p className="p-6 text-sm text-ink-muted">{t("work.loading")}</p>;
  if (!event.data) return <p className="p-6 text-sm text-danger">{t("crossover.failed")}</p>;
  const crossover = event.data;
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-4xl flex-col gap-4 p-4">
        <PageHeader
          title={crossover.name}
          subtitle={t("crossover.parts", { count: crossover.members.length })}
          actions={
            administrator && (
              <>
                <Button busy={monitor.isPending} onClick={() => monitor.mutate(!crossover.monitored)}>
                  {crossover.monitored ? t("crossover.unmonitorAll") : t("crossover.monitorAll")}
                </Button>
                <Button variant="danger" busy={remove.isPending} onClick={() => remove.mutate()}>
                  {t("crossover.delete")}
                </Button>
              </>
            )
          }
        />
        <MemberList
          members={crossover.members}
          onRemove={administrator ? (gone) => update.mutate(crossover.members.filter((member) => member.part_id !== gone.part_id)) : undefined}
        />
      </div>
    </div>
  );
}

/** A series' events and same-broadcast links, by episode. */
export function useWorkCrossovers(workId: number, enabled: boolean) {
  return useQuery({
    queryKey: ["work-crossovers", workId],
    enabled,
    queryFn: () => ok(api.GET("/works/{id}/crossovers", { params: { path: { id: workId } } })),
  });
}

/** Under an episode's title: "Event, part 2 of 4" for each event, and the other series it is the same broadcast in. */
export function CrossoverBadges({ partId, events, links, categoryId }: { partId: number; events: readonly Crossover[]; links: readonly SameContent[]; categoryId?: string }) {
  const mine = events.flatMap((event) => {
    const at = event.members.findIndex((member) => member.part_id === partId);
    return at < 0 ? [] : [{ event, at }];
  });
  const same = links.filter((link) => link.part_id === partId);
  if (mine.length === 0 && same.length === 0) return null;
  return (
    <span className="mt-0.5 flex flex-wrap items-center gap-1">
      {mine.map(({ event, at }) => (
        <Link key={event.id} to="/crossovers/$id" params={{ id: String(event.id) }}>
          <Chip>{t("crossover.badge", { name: event.name, at: at + 1, of: event.members.length })}</Chip>
        </Link>
      ))}
      {same.map((link) => (
        <Link key={link.other_part_id} {...seriesLink({ work_id: link.other_work_id }, categoryId)}>
          <Chip>{t("crossover.also", { series: link.other_work_title, ref: refLabel(link.other_ref) })}</Chip>
        </Link>
      ))}
    </span>
  );
}

/** The other series' episodes of the events whose first episode in this series is partId, on one side of it. */
export function guestMembers(partId: number, workId: number, placement: "before" | "after", events: readonly Crossover[]) {
  return events.flatMap((event) => {
    const first = event.members.findIndex((member) => member.work_id === workId);
    if (event.members[first]?.part_id !== partId) return [];
    return event.members
      .map((member, at) => ({ member, at, event }))
      .filter(({ member, at }) => member.work_id !== workId && (placement === "before" ? at < first : at > first));
  });
}

/** Around a series' first episode of an event, the event's episodes of other series in watch order, as rows of the same
 *  season: those before it above, the rest below. A crossover is watched across series, so each season shows all of it
 *  (user, 2026-10-23). */
export function CrossoverGuestRows({
  partId,
  workId,
  placement,
  events,
  categoryId,
}: {
  partId: number;
  workId: number;
  placement: "before" | "after";
  events: readonly Crossover[];
  categoryId?: string;
}) {
  const rows = guestMembers(partId, workId, placement, events);
  if (rows.length === 0) return null;
  return (
    <>
      {rows.map(({ member, at, event }) => (
        <tr key={`${event.id}:${member.part_id}`} className="border-b border-border-subtle bg-canvas/60 align-top last:border-0">
          <td className="w-9 px-3 py-1.5">
            <Link2 aria-hidden className="size-3.5 text-ink-muted" />
          </td>
          <td className="w-12 py-1.5" />
          <td className="py-1.5">
            <span className="text-ink-muted">{member.title || refLabel(member.media_ref)}</span>
            <span className="mt-0.5 flex flex-wrap items-center gap-1">
              <Link {...seriesLink(member, categoryId)}>
                <Chip>{t("crossover.guest", { series: member.work_title, ref: refLabel(member.media_ref), at: at + 1, of: event.members.length })}</Chip>
              </Link>
              <Link to="/crossovers/$id" params={{ id: String(event.id) }}>
                <Chip>{event.name}</Chip>
              </Link>
            </span>
          </td>
          <td className="hidden sm:table-cell" />
          <td className="hidden whitespace-nowrap py-1.5 pr-3 text-xs text-ink-muted md:table-cell">{member.date ?? ""}</td>
          <td className="whitespace-nowrap py-1.5 pr-3 text-right text-xs">
            <span className={member.has_file ? "text-ok" : member.monitored ? "text-danger" : "text-ink-muted"}>
              {member.has_file ? t("crossover.onDisk") : member.monitored ? t("crossover.missing") : t("crossover.unmonitored")}
            </span>
          </td>
        </tr>
      ))}
    </>
  );
}

/** An episode joins an event, new or existing, or is recorded as the same broadcast as an episode of another series. */
export function CrossoverDialog({ part, categoryId, onClose }: { part: { id: number; title?: string; media_ref: string }; categoryId: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [eventChoice, setEventChoice] = useState<string>("new");
  const [name, setName] = useState("");
  const [query, setQuery] = useState("");
  const [seriesId, setSeriesId] = useState<number>();
  const [otherPart, setOtherPart] = useState<number>();
  const events = useQuery({
    queryKey: ["crossovers", categoryId],
    queryFn: () => ok(api.GET("/crossovers", { params: { query: { category_id: Number(categoryId) } } })),
  });
  const series = useQuery({
    queryKey: ["crossover-series", categoryId, query],
    enabled: query.trim().length >= 2,
    queryFn: async () => {
      const where = JSON.stringify([{ field: "title", op: "contains", value: query.trim() }]);
      return ok(api.GET("/works", { params: { query: { category_id: Number(categoryId), where, limit: 20 } } }));
    },
  });
  const episodes = useQuery({
    queryKey: ["crossover-episodes", seriesId],
    enabled: seriesId !== undefined,
    queryFn: () => ok(api.GET("/works/{id}/parts", { params: { path: { id: seriesId! }, query: { limit: 500 } } })),
  });
  const done = () => {
    void queryClient.invalidateQueries({ queryKey: ["crossovers"] });
    void queryClient.invalidateQueries({ queryKey: ["work-crossovers"] });
    void queryClient.invalidateQueries({ queryKey: ["work"] });
    onClose();
  };
  const join = useMutation({
    mutationFn: async () => {
      if (eventChoice === "new") {
        await ok(api.POST("/crossovers", { body: { category_id: Number(categoryId), name: name.trim(), part_ids: [part.id] } }));
        return;
      }
      const event = events.data?.events.find((entry) => String(entry.id) === eventChoice);
      if (!event) return;
      await ok(api.PUT("/crossovers/{id}", {
        params: { path: { id: event.id } },
        body: { name: event.name, part_ids: [...event.members.map((member) => member.part_id).filter((id) => id !== part.id), part.id] },
      }));
    },
    onSuccess: done,
  });
  const link = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/parts/{id}/same-content", { params: { path: { id: part.id } }, body: { part_id: otherPart! } }));
    },
    onSuccess: done,
  });
  return (
    <Dialog title={t("crossover.action")} description={`${refLabel(part.media_ref)} ${part.title ?? ""}`} width="wide" onClose={onClose}>
      <div className="flex flex-col gap-5 text-sm">
        <section className="flex flex-col gap-2">
          <h3 className="font-medium">{t("crossover.joinEvent")}</h3>
          <div className="flex flex-wrap items-end gap-2">
            <select className={inputStyle} value={eventChoice} onChange={(changeEvent) => setEventChoice(changeEvent.target.value)} aria-label={t("crossover.joinEvent")}>
              <option value="new">{t("crossover.newEvent")}</option>
              {events.data?.events.map((event) => (
                <option key={event.id} value={String(event.id)}>
                  {event.name}
                </option>
              ))}
            </select>
            {eventChoice === "new" && (
              <input className={inputStyle} value={name} placeholder={t("crossover.eventName")} aria-label={t("crossover.eventName")} onChange={(changeEvent) => setName(changeEvent.target.value)} />
            )}
            <Button variant="primary" busy={join.isPending} disabled={eventChoice === "new" && !name.trim()} onClick={() => join.mutate()}>
              {t("crossover.add")}
            </Button>
          </div>
          <p className="text-xs text-ink-muted">{t("crossover.joinHelp")}</p>
        </section>
        <section className="flex flex-col gap-2">
          <h3 className="font-medium">{t("crossover.sameAs")}</h3>
          <input className={inputStyle} value={query} placeholder={t("crossover.findSeries")} aria-label={t("crossover.findSeries")} onChange={(changeEvent) => setQuery(changeEvent.target.value)} />
          {series.data && (
            <select className={inputStyle} value={seriesId ?? ""} aria-label={t("crossover.series")} onChange={(changeEvent) => setSeriesId(Number(changeEvent.target.value) || undefined)}>
              <option value="">{t("crossover.series")}</option>
              {series.data.items.map((work) => (
                <option key={work.id} value={work.id}>
                  {work.title}
                </option>
              ))}
            </select>
          )}
          {episodes.data && (
            <select className={inputStyle} value={otherPart ?? ""} aria-label={t("crossover.episode")} onChange={(changeEvent) => setOtherPart(Number(changeEvent.target.value) || undefined)}>
              <option value="">{t("crossover.episode")}</option>
              {episodes.data.items
                .filter((episode) => episode.id !== part.id)
                .map((episode) => (
                  <option key={episode.id} value={episode.id}>
                    {refLabel(episode.media_ref)} {episode.title ?? ""}
                  </option>
                ))}
            </select>
          )}
          <div>
            <Button variant="primary" icon={<Link2 aria-hidden className="size-4" />} busy={link.isPending} disabled={otherPart === undefined} onClick={() => link.mutate()}>
              {t("crossover.sameBroadcast")}
            </Button>
          </div>
          <p className="text-xs text-ink-muted">{t("crossover.sameHelp")}</p>
        </section>
        {(join.isError || link.isError) && <p className="text-danger">{t("crossover.failed")}</p>}
      </div>
    </Dialog>
  );
}
