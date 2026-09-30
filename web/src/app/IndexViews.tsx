import type { LinkProps } from "@tanstack/react-router";
import { Link } from "@tanstack/react-router";
import * as DropdownMenu from "@radix-ui/react-dropdown-menu";
import { Check, LayoutGrid, Rows3, Settings2, StretchHorizontal } from "lucide-react";

import { albumStatusFill, albumStatusLabel, type AlbumStatus } from "@/lib/albumStatus";
import type { components } from "@/api/schema";
import type { useLibraryList } from "@/list/useLibraryList";

import { CoverArt } from "./CoverArt";
import { ActionButtons, MonitorToggle, useItemActions } from "./LevelItemActions";
import { ArtistCoverMark, Poster } from "./Poster";
import { formatBytes } from "./Queue";

/** One album or artist as Radarr's index views show it (asked 2026-09-30), whichever list it came from. */
export interface IndexEntry {
  id: number;
  kind: "albums" | "artists" | "tracks";
  title: string;
  subtitle?: string;
  year?: number;
  status?: AlbumStatus;
  presentCount?: number;
  requiredCount?: number;
  monitored?: boolean;
  overview?: string;
  sizeBytes?: number;
  artistCover?: boolean;
  image: { subject: "works" | "people"; id: number } | null;
  link: LinkProps | null;
}

export type IndexView = "poster" | "overview" | "list";

type Work = components["schemas"]["Work"];

/** An album from the works list as the index views show it. */
export function workEntry(work: Work): IndexEntry {
  return {
    id: work.id, kind: "albums", title: work.title,
    subtitle: [work.artist ?? work.media_type, work.year, work.unverified ? "unverified" : null].filter(Boolean).join(" · "),
    year: work.year, status: work.status, presentCount: work.present_count, requiredCount: work.required_count,
    monitored: work.monitored, overview: work.overview, sizeBytes: work.size_bytes, artistCover: work.artist_cover,
    image: { subject: "works", id: work.id }, link: { to: "/works/$id", params: { id: String(work.id) } },
  };
}

export const POSTER_WIDTHS = { small: 128, medium: 168, large: 224 } as const;
export type PosterSize = keyof typeof POSTER_WIDTHS;
export const POSTER_GAP = 16;
export const POSTER_PADDING = 16;
export const OVERVIEW_ROW_HEIGHT = 168;
const POSTER_LABEL_HEIGHT = 46;
const POSTER_BAR_HEIGHT = 10;
const POSTER_SEARCH_HEIGHT = 34;

/** Radarr's poster Options: size and what each tile shows, remembered per person. */
export interface PosterOptions {
  size: PosterSize;
  showTitle: boolean;
  showMonitored: boolean;
  showAvailability: boolean;
  showSearch: boolean;
}
export const DEFAULT_POSTER_OPTIONS: PosterOptions = {
  size: "medium", showTitle: true, showMonitored: true, showAvailability: true, showSearch: false,
};

/** A poster row's height for the options chosen: the square cover, then what is shown under it. */
export function posterRowHeight(options: PosterOptions, cellWidth: number): number {
  return (
    cellWidth + POSTER_GAP + (options.showTitle ? POSTER_LABEL_HEIGHT : 0) +
    (options.showAvailability ? POSTER_BAR_HEIGHT : 0) + (options.showSearch ? POSTER_SEARCH_HEIGHT : 0)
  );
}

const actionable = (entry: IndexEntry) => entry.kind === "albums" || entry.kind === "artists";

/** One poster with Radarr's bookmark and hover actions; a component of its own, since the actions are hooks. */
export function IndexPoster({ entry, options }: { entry: IndexEntry; options: PosterOptions }) {
  const actions = useItemActions(entry.kind, entry.id, entry.monitored);
  return (
    <div>
      <Poster
        subject={entry.image?.subject ?? "works"}
        imageId={entry.image?.id ?? null}
        title={entry.title}
        subtitle={entry.subtitle ?? (entry.year != null ? String(entry.year) : undefined)}
        link={entry.link}
        badge={entry.artistCover ? <ArtistCoverMark /> : undefined}
        topLeft={options.showMonitored ? <MonitorToggle actions={actions} /> : undefined}
        hoverActions={actionable(entry) ? <ActionButtons actions={actions} showNote={false} /> : undefined}
        showTitle={options.showTitle}
        round={entry.kind === "artists"}
        status={options.showAvailability ? entry.status : undefined}
        presentCount={entry.presentCount}
        requiredCount={entry.requiredCount}
      />
      {options.showSearch && actionable(entry) && (
        <button
          type="button"
          disabled={actions.search.isPending}
          onClick={() => actions.search.mutate()}
          className="mt-1.5 w-full truncate rounded-md border border-border-subtle py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50"
          title={actions.note}
        >
          {actions.note ?? "Search"}
        </button>
      )}
    </div>
  );
}

const menuItem = "flex cursor-default items-center gap-2 rounded px-2 py-1 outline-none data-[highlighted]:bg-canvas";

/** Radarr's poster Options menu. */
export function PosterOptionsMenu({ options, onChange }: { options: PosterOptions; onChange: (next: PosterOptions) => void }) {
  const toggles: readonly { key: "showTitle" | "showMonitored" | "showAvailability" | "showSearch"; label: string }[] = [
    { key: "showTitle", label: "Show title" },
    { key: "showMonitored", label: "Show monitored" },
    { key: "showAvailability", label: "Show availability bar" },
    { key: "showSearch", label: "Show search button" },
  ];
  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger aria-label="Poster options" title="Poster options" className="rounded-md border border-border-subtle p-1.5 text-ink-muted hover:text-ink">
        <Settings2 aria-hidden className="size-4" />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content align="end" sideOffset={4} className="z-50 min-w-48 rounded-md border border-border-subtle bg-surface p-1 text-sm shadow-lg">
          <DropdownMenu.Label className="px-2 py-1 text-xs text-ink-muted">Poster size</DropdownMenu.Label>
          <DropdownMenu.RadioGroup value={options.size} onValueChange={(size) => onChange({ ...options, size: size as PosterSize })}>
            {(Object.keys(POSTER_WIDTHS) as PosterSize[]).map((size) => (
              <DropdownMenu.RadioItem key={size} value={size} onSelect={(event) => event.preventDefault()} className={`${menuItem} capitalize`}>
                <span className="size-3.5">
                  <DropdownMenu.ItemIndicator>
                    <Check aria-hidden className="size-3.5" />
                  </DropdownMenu.ItemIndicator>
                </span>
                {size}
              </DropdownMenu.RadioItem>
            ))}
          </DropdownMenu.RadioGroup>
          <DropdownMenu.Separator className="my-1 h-px bg-border-subtle" />
          {toggles.map((toggle) => (
            <DropdownMenu.CheckboxItem
              key={toggle.key}
              checked={options[toggle.key]}
              onSelect={(event) => event.preventDefault()}
              onCheckedChange={(checked) => onChange({ ...options, [toggle.key]: checked })}
              className={menuItem}
            >
              <span className="size-3.5">
                <DropdownMenu.ItemIndicator>
                  <Check aria-hidden className="size-3.5" />
                </DropdownMenu.ItemIndicator>
              </span>
              {toggle.label}
            </DropdownMenu.CheckboxItem>
          ))}
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  );
}

/** Radarr's three views, as a button group. */
export function IndexViewToggle({ view, onChange }: { view: IndexView; onChange: (view: IndexView) => void }) {
  const modes = [
    { mode: "poster" as const, icon: LayoutGrid, label: "Posters" },
    { mode: "overview" as const, icon: StretchHorizontal, label: "Overview" },
    { mode: "list" as const, icon: Rows3, label: "Table" },
  ];
  return (
    <div className="flex items-center rounded-md border border-border-subtle">
      {modes.map(({ mode, icon: Icon, label }) => (
        <button
          key={mode}
          type="button"
          aria-pressed={view === mode}
          aria-label={label}
          title={label}
          onClick={() => onChange(mode)}
          className={`p-1.5 first:rounded-l-md last:rounded-r-md focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
            view === mode ? "bg-accent text-accent-ink" : "text-ink-muted hover:bg-canvas hover:text-ink"
          }`}
        >
          <Icon aria-hidden className="size-4" />
        </button>
      ))}
    </div>
  );
}

/** Radarr's Overview: a cover, the name, a paragraph about it and its details, a row each. */
export function OverviewList<T>({
  list,
  entryOf,
}: {
  list: ReturnType<typeof useLibraryList<T>>;
  entryOf: (item: T) => IndexEntry;
}) {
  return (
    <div ref={list.scrollRef} className="h-full overflow-auto">
      <div style={{ height: list.totalSize, position: "relative" }}>
        {list.virtualItems.map((virtual) => {
          const item = list.items[virtual.index];
          if (!item) return null;
          const entry = entryOf(item);
          return (
            <div key={entry.id} className="absolute inset-x-0 px-4" style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}>
              <OverviewRow entry={entry} />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function OverviewRow({ entry }: { entry: IndexEntry }) {
  const actions = useItemActions(entry.kind, entry.id, entry.monitored);
  const details = [
    entry.year != null ? String(entry.year) : null,
    entry.requiredCount != null
      ? `${entry.presentCount ?? 0} of ${entry.requiredCount} ${entry.kind === "artists" ? "albums" : "tracks"}`
      : null,
    entry.status ? albumStatusLabel(entry.status) : null,
    entry.sizeBytes ? formatBytes(entry.sizeBytes) : null,
  ].filter((detail): detail is string => detail !== null);
  const body = (
    <div className="flex h-full gap-4 border-b border-border-subtle py-3">
      <div className="relative h-full shrink-0" style={{ width: OVERVIEW_ROW_HEIGHT - 24 }}>
        {entry.image && (
          <CoverArt
            subject={entry.image.subject}
            workId={entry.image.id}
            title={entry.title}
            size="small"
            className={`size-full border border-border-subtle object-cover ${entry.kind === "artists" ? "rounded-full" : "rounded-md"}`}
          />
        )}
        {entry.artistCover && (
          <span className="absolute right-1 top-1">
            <ArtistCoverMark />
          </span>
        )}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex items-center gap-2">
          <MonitorToggle actions={actions} />
          <span className="truncate text-base font-medium">{entry.title}</span>
          {entry.subtitle && <span className="truncate text-sm text-ink-muted">{entry.subtitle}</span>}
          {actionable(entry) && (
            <span className="ml-auto shrink-0">
              <ActionButtons actions={actions} />
            </span>
          )}
        </div>
        {entry.overview && <p className="line-clamp-3 text-sm text-ink-muted">{entry.overview}</p>}
        <div className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-muted">
          {entry.status && <span className={`size-2 rounded-full ${albumStatusFill(entry.status)}`} />}
          {details.map((detail) => (
            <span key={detail}>{detail}</span>
          ))}
        </div>
      </div>
    </div>
  );
  return entry.link ? (
    <Link {...entry.link} className="block h-full hover:bg-surface">
      {body}
    </Link>
  ) : (
    body
  );
}

/** A table row's monitored bookmark, or its search and refresh buttons. */
export function RowActions({ column, entry }: { column: "monitored" | "actions"; entry: IndexEntry }) {
  const actions = useItemActions(entry.kind, entry.id, entry.monitored);
  return column === "monitored" ? <MonitorToggle actions={actions} /> : <ActionButtons actions={actions} showNote={false} />;
}

/** Posters for a paged list (Wanted), unvirtualised: one page is at most a hundred. */
export function PosterWall({ entries, options }: { entries: readonly IndexEntry[]; options: PosterOptions }) {
  return (
    <div className="grid gap-4 p-4" style={{ gridTemplateColumns: `repeat(auto-fill, minmax(${POSTER_WIDTHS[options.size]}px, 1fr))` }}>
      {entries.map((entry) => (
        <IndexPoster key={entry.id} entry={entry} options={options} />
      ))}
    </div>
  );
}

/** The Overview for a paged list (Wanted), unvirtualised. */
export function OverviewRows({ entries }: { entries: readonly IndexEntry[] }) {
  return (
    <div className="px-4">
      {entries.map((entry) => (
        <div key={entry.id} style={{ height: OVERVIEW_ROW_HEIGHT }}>
          <OverviewRow entry={entry} />
        </div>
      ))}
    </div>
  );
}
