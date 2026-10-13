import { useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Bookmark, BookmarkCheck, ChevronDown, ChevronRight, Search, Trash2, User } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import type { MediaTypeInfo } from "@/lib/useMediaTypes";
import { t, type Vocabulary } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { ConfirmDialog } from "@/ui/ConfirmDialog";

import { fileChips, formatDuration, formatMinutes, partState, type PartState } from "./workFormat";

type Part = components["schemas"]["WorkPart"];
type Group = components["schemas"]["WorkGroup"];
type Credit = components["schemas"]["Credit"];

/** Every part of a work, or of one group, a page at a time until all are here; nothing is fetched until enabled. */
export function useAllParts(workId: number, group: string | undefined, enabled = true) {
  const parts = useInfiniteQuery({
    queryKey: ["work", workId, "parts", group ?? null],
    enabled,
    initialPageParam: "",
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET("/works/{id}/parts", {
        params: { path: { id: workId }, query: { limit: 200, ...(group !== undefined ? { group } : {}), ...(pageParam ? { cursor: pageParam } : {}) } },
      });
      if (error) throw error;
      return data;
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  const { hasNextPage, isFetchingNextPage, fetchNextPage } = parts;
  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage) void fetchNextPage();
  }, [hasNextPage, isFetchingNextPage, fetchNextPage]);
  return { parts: (parts.data?.pages ?? []).flatMap((page) => page.items), loading: parts.isLoading, error: parts.isError };
}

// How long a part is waited for after its time, by how sure the time is; the server's own table (library.PartOut).
const GRACE_HOURS: Record<string, number> = { high: 2, medium: 12 };

/** Where a part's time comes from and when it starts to count as missing, for the reader who doubts it. */
function outNote(part: Part): string | undefined {
  if (!part.available_basis) return undefined;
  if (part.available_basis === "unannounced") return t("work.outUnannounced");
  return t("work.outBasis", { basis: part.available_basis, hours: GRACE_HOURS[part.available_confidence ?? ""] ?? 36 });
}

const STATE: Record<PartState, { label: string; tone: string }> = {
  present: { label: "On disk", tone: "text-ok" },
  missing: { label: "Missing", tone: "text-danger" },
  upcoming: { label: "Not out yet", tone: "text-ink-muted" },
  unmonitored: { label: "Unmonitored", tone: "text-ink-muted" },
};

/** What a part row may do; an absent action is one the viewer may not take. */
export interface PartActions {
  onMonitor?: (part: Part, monitored: boolean) => void;
  /** Search one part on its own: only a type that narrows a group's search to a part (an episode) offers it. */
  onGrab?: (part: Part, group: Group) => void;
  onInteractive?: (part: Part, group: Group) => void;
  onRemove?: (part: Part, deleteFiles: boolean) => void;
}

/** Sonarr's season: a header with its monitoring, counts and searches, opening to its episodes. */
export function GroupSection({
  workId,
  group,
  initiallyOpen,
  categoryId,
  info,
  words,
  actions,
  onGroupMonitor,
  onGroupGrab,
  onGroupInteractive,
  note,
}: {
  workId: number;
  group: Group;
  initiallyOpen: boolean;
  categoryId: string | undefined;
  info: MediaTypeInfo | undefined;
  words: Vocabulary;
  actions: PartActions;
  onGroupMonitor?: (monitored: boolean) => void;
  onGroupGrab?: () => void;
  onGroupInteractive?: () => void;
  note?: string;
}) {
  const [open, setOpen] = useState(initiallyOpen);
  const Chevron = open ? ChevronDown : ChevronRight;
  const { parts, loading } = useAllParts(workId, group.ref, open);
  return (
    <section className="rounded-lg border border-border-subtle bg-surface">
      <header className="flex items-center gap-3 px-3 py-2">
        <MonitorToggle monitored={group.monitored} label={group.label} onChange={onGroupMonitor} />
        <button type="button" onClick={() => setOpen(!open)} aria-expanded={open} className="flex flex-1 items-center gap-2 text-left">
          <Chevron aria-hidden className="size-4 text-ink-muted" />
          <span className="text-sm font-semibold">{group.label}</span>
          <span className="text-xs text-ink-muted">
            {group.present_count}
            {group.required_count ? `/${Math.max(group.required_count, group.present_count)}` : ""}
          </span>
        </button>
        {note && <span className="text-xs text-ink-muted">{note}</span>}
        {onGroupGrab && (
          <Button variant="icon" title={`Search ${group.label} and send the best release`} aria-label={`${t("action.search")} ${group.label}`} onClick={onGroupGrab}>
            <Search aria-hidden className="size-4" />
          </Button>
        )}
        {onGroupInteractive && (
          <Button variant="icon" title="Choose the release yourself" aria-label={`${t("action.interactive")} ${group.label}`} onClick={onGroupInteractive}>
            <User aria-hidden className="size-4" />
          </Button>
        )}
      </header>
      {open && (
        <div className="border-t border-border-subtle">
          {loading ? (
            <p className="px-3 py-2 text-xs text-ink-muted">{t("work.loading")}</p>
          ) : (
            <PartTable parts={parts} group={group} categoryId={categoryId} info={info} words={words} actions={actions} />
          )}
        </div>
      )}
    </section>
  );
}

/** Lidarr's tracklist or a season's episodes: one row per part, under its section's heading when there are several. */
export function PartTable({
  parts,
  group,
  categoryId,
  info,
  words,
  actions,
}: {
  parts: readonly Part[];
  group: Group | undefined;
  categoryId: string | undefined;
  info: MediaTypeInfo | undefined;
  words: Vocabulary;
  actions: PartActions;
}) {
  if (parts.length === 0) return <p className="px-3 py-2 text-xs text-ink-muted">{t("work.noParts", words)}</p>;
  const sections = [...new Set(parts.map((part) => part.section ?? ""))];
  const headed = sections.length > 1;
  return (
    <table className="w-full text-sm">
      {sections.map((section) => (
        <tbody key={section}>
          {headed && (
            <tr>
              <th colSpan={6} className="bg-canvas px-3 py-1 text-left text-xs font-medium text-ink-muted">
                {section}
              </th>
            </tr>
          )}
          {parts
            .filter((part) => (part.section ?? "") === section)
            .map((part) => (
              <PartRow key={part.id} part={part} group={group} categoryId={categoryId} info={info} actions={actions} />
            ))}
        </tbody>
      ))}
    </table>
  );
}

function PartRow({
  part,
  group,
  categoryId,
  info,
  actions,
}: {
  part: Part;
  group: Group | undefined;
  categoryId: string | undefined;
  info: MediaTypeInfo | undefined;
  actions: PartActions;
}) {
  const [removing, setRemoving] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const state = STATE[partState(part)];
  const title = part.title ?? part.media_ref;
  const out = part.available_at
    ? new Date(part.available_at).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })
    : part.date;
  const absolute = part.alt_numbers?.absolute;
  return (
    <tr className="border-b border-border-subtle last:border-0 align-top">
      <td className="w-9 px-3 py-1.5">
        <MonitorToggle monitored={part.monitored} label={title} onChange={actions.onMonitor && ((monitored) => actions.onMonitor?.(part, monitored))} small />
      </td>
      <td className="w-12 py-1.5 tabular-nums text-ink-muted">{part.number ?? part.media_ref}</td>
      <td className="py-1.5">
        <span>{title}</span>
        {absolute && <span className="ml-2 text-xs text-ink-muted">#{absolute}</span>}
        {part.file && (
          <span className="mt-0.5 flex flex-wrap items-center gap-1">
            {fileChips(part.file).map((chip) => (
              <Chip key={chip}>{chip}</Chip>
            ))}
            {part.file.meets_target != null && (
              <Chip tone={part.file.meets_target ? "ok" : "warn"}>{part.file.meets_target ? "meets the target" : "below the target"}</Chip>
            )}
            {part.file.meets_cutoff === false && <Chip tone="warn">below the cutoff</Chip>}
          </span>
        )}
        <PartCredits credits={part.credits ?? []} categoryId={categoryId} info={info} />
      </td>
      <td className="hidden whitespace-nowrap py-1.5 pr-3 text-xs text-ink-muted sm:table-cell">
        {part.runtime_ms ? (part.runtime_ms >= 10 * 60_000 ? formatMinutes(part.runtime_ms) : formatDuration(part.runtime_ms)) : ""}
      </td>
      <td className="hidden whitespace-nowrap py-1.5 pr-3 text-xs text-ink-muted md:table-cell" title={outNote(part)}>
        {out ?? ""}
        {out && part.available_basis && part.available_confidence !== "high" && " ≈"}
      </td>
      <td className="whitespace-nowrap py-1.5 pr-3 text-right text-xs">
        <span className={state.tone}>{state.label}</span>
        {group && actions.onGrab && (
          <Button variant="icon" className="ml-2" title="Search for it and send the best release" aria-label={`${t("action.search")} ${title}`} onClick={() => actions.onGrab?.(part, group)}>
            <Search aria-hidden className="size-3.5" />
          </Button>
        )}
        {group && actions.onInteractive && (
          <Button variant="icon" title="Choose the release yourself" aria-label={`${t("action.interactive")} ${title}`} onClick={() => actions.onInteractive?.(part, group)}>
            <User aria-hidden className="size-3.5" />
          </Button>
        )}
        {actions.onRemove && (
          <Button variant="icon" title="Stop wanting it" aria-label={`${t("action.remove")} ${title}`} onClick={() => setRemoving(true)}>
            <Trash2 aria-hidden className="size-3.5" />
          </Button>
        )}
        {removing && actions.onRemove && (
          <ConfirmDialog
            title={t("work.remove.title", { title })}
            consequence="It is no longer wanted. Its file stays unless you also delete it."
            confirmLabel={t("action.remove")}
            danger
            onCancel={() => setRemoving(false)}
            onConfirm={() => {
              actions.onRemove?.(part, deleteFiles && part.file != null);
              setRemoving(false);
            }}
          >
            {part.file && (
              <label className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={deleteFiles} onChange={(event) => setDeleteFiles(event.target.checked)} />
                {t("work.remove.deleteFiles")}
              </label>
            )}
          </ConfirmDialog>
        )}
      </td>
    </tr>
  );
}

/** A part's own people under its title, one line per role as the type names them: "Director Ann · Guest star Bo (Cy)". */
function PartCredits({ credits, categoryId, info }: { credits: readonly Credit[]; categoryId: string | undefined; info: MediaTypeInfo | undefined }) {
  if (credits.length === 0) return null;
  const roles = [...new Set(credits.map((credit) => credit.role))];
  const label = (role: string) => info?.roles.find((entry) => entry.key === role)?.label ?? role.replace(/_/g, " ");
  return (
    <span className="mt-0.5 block text-xs text-ink-muted">
      {roles.map((role, index) => (
        <span key={role}>
          {index > 0 && " · "}
          {label(role)}{" "}
          {credits
            .filter((credit) => credit.role === role)
            .slice(0, 5)
            .map((credit, position) => (
              <span key={`${credit.person_id}-${credit.character ?? ""}`}>
                {position > 0 && ", "}
                <PersonLink id={credit.person_id} name={credit.person_name} categoryId={categoryId} />
                {credit.character ? ` (${credit.character})` : ""}
              </span>
            ))}
        </span>
      ))}
    </span>
  );
}

/** A person's page, inside the library when there is one. */
export function PersonLink({ id, name, categoryId }: { id: number; name: string; categoryId: string | undefined }) {
  const link = categoryId
    ? ({ to: "/library/$categoryId/people/$personId", params: { categoryId, personId: String(id) } } as const)
    : ({ to: "/people/$id", params: { id: String(id) } } as const);
  return (
    <Link {...link} className="hover:text-accent hover:underline">
      {name}
    </Link>
  );
}

/** The bookmark Sonarr and Radarr monitor with; read-only without the right to change it. */
export function MonitorToggle({ monitored, label, onChange, small = false }: { monitored: boolean; label: string; onChange?: (monitored: boolean) => void; small?: boolean }) {
  const size = small ? "size-3.5" : "size-4";
  return (
    <Button
      variant="icon"
      aria-pressed={monitored}
      aria-label={`${monitored ? t("action.unmonitor") : t("action.monitor")} ${label}`}
      disabled={!onChange}
      onClick={() => onChange?.(!monitored)}
    >
      {monitored ? <BookmarkCheck aria-hidden className={`${size} text-accent`} /> : <Bookmark aria-hidden className={size} />}
    </Button>
  );
}
