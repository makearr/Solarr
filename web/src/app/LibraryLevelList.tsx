import { Link, useParams } from "@tanstack/react-router";
import type { LinkProps } from "@tanstack/react-router";

import { api } from "@/api/client";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import { useRecordLastLevel } from "@/list/useRecordLastLevel";
import type { components } from "@/api/schema";
import { CoverArt } from "./CoverArt";

type LevelItem = components["schemas"]["LibraryLevelItem"];

const ROW_HEIGHT = 44;
const FILTER_KEYS: readonly string[] = [];

/** Which /{subject}/{id}/artwork this row's own image comes from, or null
 *  for a level with no image source at all -- Artists reads the person's
 *  own id directly; Tracks reads its own album's work_id, so a song falls
 *  back to its album's cover (which itself falls back server-side to the
 *  artist's, GetWorkArtwork's own chain) rather than needing a source of
 *  its own, since no track-level image concept exists (2026-09-11). */
function imageSourceFor(level: string | undefined, item: LevelItem): { subject: "works" | "people"; id: number } | null {
  if (level === "artists") return { subject: "people", id: item.id };
  if (item.work_id != null) return { subject: "works", id: item.work_id };
  return null;
}

/** Where a row navigates: Artists to the person page, Tracks to their own
 *  album (there is no track-detail page -- a track is a work_part shown
 *  inside its work, WorkDetail's own Parts section). null for a level with
 *  neither a person nor a work to land on. */
function linkFor(level: string | undefined, item: LevelItem): LinkProps | null {
  if (level === "artists") return { to: "/people/$id", params: { id: String(item.id) } };
  if (item.work_id != null) return { to: "/works/$id", params: { id: String(item.work_id) } };
  return null;
}

/** The generic browse view for any navigation level besides Albums (ADR-54)
 *  -- Tracks today, a future Artists once a reader exists. One generic row
 *  shape across every entity: only one media type exists yet to generalize a
 *  typed-per-level response from (GET /libraries/{id}/{level}'s own spec). */
export function LibraryLevelList() {
  const { categoryId, level } = useParams({ strict: false }) as {
    categoryId?: string;
    level?: string;
  };
  useRecordLastLevel(categoryId, level ?? "");

  const list = useLibraryList<LevelItem>(
    {
      name: `library-level:${categoryId ?? ""}:${level ?? ""}`,
      rowHeight: ROW_HEIGHT,
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
    <div className="flex h-full flex-col">
      {list.isLoading && <Message>Loading…</Message>}
      {list.error !== null && (
        <Message>{(list.error as { detail?: string }).detail ?? "This view could not be read."}</Message>
      )}
      {!list.isLoading && list.error === null && list.items.length === 0 && (
        <Message>Nothing here yet.</Message>
      )}

      {list.items.length > 0 && (
        <div ref={list.scrollRef} className="min-h-0 flex-1 overflow-auto">
          <div style={{ height: list.totalSize, position: "relative" }}>
            {list.virtualItems.map((virtual) => {
              const item = list.items[virtual.index];
              if (!item) return null;
              const image = imageSourceFor(level, item);
              const link = linkFor(level, item);
              const rowClass =
                "absolute inset-x-0 flex items-center gap-3 border-b border-border-subtle px-4 text-sm";
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
                  {item.subtitle && (
                    <span className="min-w-0 flex-1 truncate text-ink-muted">{item.subtitle}</span>
                  )}
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
      )}
    </div>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
