import type { LibraryList } from "./useLibraryList";
import type { Id } from "./selection";

/** The poster wall (Lidarr's own artist and album view, asked for repeatedly
 *  and finally built 2026-09-13). Virtualised a row of posters at a time
 *  through the same core the table uses -- the old "cover view" was a
 *  one-per-row list with a 48px thumbnail, which is why it never read as a
 *  poster wall at all. */
export function PosterGrid<T>({
  list,
  idOf,
  renderPoster,
}: {
  list: LibraryList<T>;
  idOf: (item: T) => Id;
  renderPoster: (item: T) => React.ReactNode;
}) {
  const perRow = list.perRow;

  return (
    <div ref={list.scrollRef} className="h-full overflow-auto p-4">
      <div style={{ height: list.totalSize, position: "relative" }}>
        {list.virtualItems.map((virtual) => {
          const start = virtual.index * perRow;
          const row = list.items.slice(start, start + perRow);
          if (row.length === 0) return null;

          return (
            <div
              key={virtual.index}
              className="absolute inset-x-0 grid gap-4"
              style={{
                gridTemplateColumns: `repeat(${perRow}, minmax(0, 1fr))`,
                height: virtual.size,
                transform: `translateY(${virtual.start}px)`,
              }}
            >
              {row.map((item) => (
                <div key={idOf(item)}>{renderPoster(item)}</div>
              ))}
            </div>
          );
        })}
      </div>
    </div>
  );
}
