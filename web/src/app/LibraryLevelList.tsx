import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import type { LinkProps } from "@tanstack/react-router";
import { LayoutGrid, Plus, Rows3 } from "lucide-react";

import { api } from "@/api/client";
import { useLibraryList } from "@/list/useLibraryList";
import { useGridMetrics } from "@/list/useGridMetrics";
import { PosterGrid } from "@/list/PosterGrid";
import { useRouteView } from "@/list/useRouteView";
import { useRecordLastLevel } from "@/list/useRecordLastLevel";
import type { components } from "@/api/schema";
import { CoverArt } from "./CoverArt";
import { Poster } from "./Poster";

type LevelItem = components["schemas"]["LibraryLevelItem"];

const ROW_HEIGHT = 44;
const FILTER_KEYS: readonly string[] = [];

// The poster wall's own geometry: a tile is at least this wide, gaps and page
// padding are the grid's, and a row is one square cover plus its two lines of
// label.
const POSTER_MIN_WIDTH = 168;
const POSTER_GAP = 16;
const POSTER_PADDING = 16;
const POSTER_LABEL_HEIGHT = 46;

/** Which levels open as a poster wall rather than a list -- the browse views
 *  a person actually looks at (Lidarr's own Artists and Albums), not Tracks,
 *  where a cover per row says nothing a tracklist does not. */
const POSTER_LEVELS = new Set(["artists", "albums"]);

type ViewMode = "poster" | "list";
const VIEW_STORAGE_KEY = "solarr.level.view";

/** The remembered poster/list choice per level, so switching to the list on
 *  Tracks does not also flatten the Artists wall. */
function useViewMode(level: string | undefined): [ViewMode, (mode: ViewMode) => void] {
  const fallback: ViewMode = level && POSTER_LEVELS.has(level) ? "poster" : "list";
  const [mode, setMode] = useState<ViewMode>(fallback);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(`${VIEW_STORAGE_KEY}.${level ?? ""}`);
      setMode(raw === "poster" || raw === "list" ? raw : fallback);
    } catch {
      setMode(fallback);
    }
  }, [level, fallback]);

  const choose = (next: ViewMode) => {
    setMode(next);
    try {
      localStorage.setItem(`${VIEW_STORAGE_KEY}.${level ?? ""}`, next);
    } catch {
      // A browser refusing storage costs the memory of the choice, nothing more.
    }
  };

  return [mode, choose];
}

/** Which /{subject}/{id}/artwork this row's own image comes from, or null
 *  for a level with no image source at all -- Artists reads the person's
 *  own id directly; Tracks reads its own album's work_id, so a song falls
 *  back to its album's cover (which itself falls back server-side to the
 *  artist's, GetWorkArtwork's own chain). */
function imageSourceFor(level: string | undefined, item: LevelItem): { subject: "works" | "people"; id: number } | null {
  if (level === "artists") return { subject: "people", id: item.id };
  if (item.work_id != null) return { subject: "works", id: item.work_id };
  return null;
}

/** Where a row navigates: Artists to the person page, Tracks to their own
 *  album (there is no track-detail page -- a track is a work_part shown
 *  inside its work, WorkDetail's own Parts section). */
function linkFor(level: string | undefined, item: LevelItem): LinkProps | null {
  if (level === "artists") return { to: "/people/$id", params: { id: String(item.id) } };
  if (item.work_id != null) return { to: "/works/$id", params: { id: String(item.work_id) } };
  return null;
}

/** The browse view for any navigation level (ADR-54). Artists and Albums
 *  open as a poster wall; every level can be switched to a compact list. */
export function LibraryLevelList() {
  const { categoryId, level } = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
  };
  useRecordLastLevel(categoryId, level ?? "");

  const [view, setView] = useViewMode(level);
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_MIN_WIDTH, POSTER_GAP, POSTER_PADDING);
  const isPoster = view === "poster";

  const list = useLibraryList<LevelItem>(
    {
      name: `library-level:${categoryId ?? ""}:${level ?? ""}`,
      rowHeight: isPoster ? metrics.cellWidth + POSTER_LABEL_HEIGHT + POSTER_GAP : ROW_HEIGHT,
      itemsPerRow: isPoster ? metrics.columns : 1,
      idOf: (item) => item.id,
      fetchPage: async ({ cursor, signal }) => {
        const { data, error } = await api.GET("/libraries/{id}/{level}", {
          params: {
            path: { id: Number(categoryId), level: level ?? "" },
            query: { cursor, limit: 100 },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    useRouteView(FILTER_KEYS),
  );

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex flex-wrap items-center gap-2 border-b border-border-subtle px-4 py-2">
        <h1 className="text-sm font-semibold capitalize tracking-tight">{level ?? "Library"}</h1>
        <div className="ml-auto flex items-center gap-1">
          <ViewToggle view={view} onChange={setView} />
          {categoryId && (
            <Link
              to="/library/$categoryId/discover"
              params={{ categoryId }}
              className="ml-2 flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <Plus aria-hidden className="size-4" />
              Add new
            </Link>
          )}
        </div>
      </div>

      {list.isLoading && <Message>Loading…</Message>}
      {list.error !== null && (
        <Message>{(list.error as { detail?: string }).detail ?? "This view could not be read."}</Message>
      )}
      {!list.isLoading && list.error === null && list.items.length === 0 && <Message>Nothing here yet.</Message>}

      {list.items.length > 0 && (
        <div className="min-h-0 flex-1">
          {isPoster ? (
            <PosterGrid
              list={list}
              idOf={(item) => item.id}
              renderPoster={(item) => {
                const image = imageSourceFor(level, item);
                return (
                  <Poster
                    subject={image?.subject ?? "works"}
                    imageId={image?.id ?? null}
                    title={item.title}
                    subtitle={item.subtitle ?? (item.year != null ? String(item.year) : undefined)}
                    link={linkFor(level, item)}
                    round={level === "artists"}
                  />
                );
              }}
            />
          ) : (
            <CompactList list={list} level={level} />
          )}
        </div>
      )}
    </div>
  );
}

function ViewToggle({ view, onChange }: { view: ViewMode; onChange: (mode: ViewMode) => void }) {
  return (
    <div className="flex items-center rounded-md border border-border-subtle">
      {(
        [
          { mode: "poster" as const, icon: LayoutGrid, label: "Poster view" },
          { mode: "list" as const, icon: Rows3, label: "List view" },
        ]
      ).map(({ mode, icon: Icon, label }) => (
        <button
          key={mode}
          type="button"
          aria-pressed={view === mode}
          aria-label={label}
          title={label}
          onClick={() => onChange(mode)}
          className={`p-1.5 first:rounded-l-md last:rounded-r-md focus-visible:outline-2
                      focus-visible:outline-offset-2 focus-visible:outline-accent ${
                        view === mode ? "bg-accent text-accent-ink" : "text-ink-muted hover:bg-canvas hover:text-ink"
                      }`}
        >
          <Icon aria-hidden className="size-4" />
        </button>
      ))}
    </div>
  );
}

function CompactList({
  list,
  level,
}: {
  list: ReturnType<typeof useLibraryList<LevelItem>>;
  level: string | undefined;
}) {
  return (
    <div ref={list.scrollRef} className="h-full overflow-auto">
      <div style={{ height: list.totalSize, position: "relative" }}>
        {list.virtualItems.map((virtual) => {
          const item = list.items[virtual.index];
          if (!item) return null;
          const image = imageSourceFor(level, item);
          const link = linkFor(level, item);
          const rowClass = "absolute inset-x-0 flex items-center gap-3 border-b border-border-subtle px-4 text-sm";
          const row = (
            <>
              {image && (
                <CoverArt
                  subject={image.subject}
                  workId={image.id}
                  title={item.title}
                  size="thumb"
                  className={`h-8 w-8 shrink-0 object-cover ${
                    image.subject === "people" ? "rounded-full" : "rounded"
                  }`}
                />
              )}
              <span className="min-w-0 flex-1 truncate font-medium">{item.title}</span>
              {item.subtitle && <span className="min-w-0 flex-1 truncate text-ink-muted">{item.subtitle}</span>}
              {item.year != null && <span className="shrink-0 text-ink-muted">{item.year}</span>}
            </>
          );
          const style = { height: virtual.size, transform: `translateY(${virtual.start}px)` };
          return link ? (
            <Link key={item.id} {...link} className={`${rowClass} hover:bg-surface`} style={style}>
              {row}
            </Link>
          ) : (
            <div key={item.id} className={rowClass} style={style}>
              {row}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
