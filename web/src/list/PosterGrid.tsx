import type { LibraryList } from "./useLibraryList";
import type { Id } from "./selection";

/** The poster wall (Lidarr's own artist and album view, asked for repeatedly
 *  and finally built 2026-09-13). Virtualised a row of posters at a time
 *  through the same core the table uses. A grouped list (the hook's groupOf)
 *  adds a header row above each group. */
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
          const placement = { height: virtual.size, transform: `translateY(${virtual.start}px)` };
          const layoutRow = list.layout?.[virtual.index];

          if (layoutRow?.kind === "group") {
            return (
              <h2
                key={`group-${virtual.index}`}
                className="absolute inset-x-0 flex items-end border-b border-border-subtle pb-1.5
                           text-sm font-semibold tracking-tight"
                style={placement}
              >
                {layoutRow.label}
              </h2>
            );
          }

          const start = layoutRow ? layoutRow.start : virtual.index * perRow;
          const end = layoutRow ? layoutRow.end : start + perRow;
          const row = list.items.slice(start, end);
          if (row.length === 0) return null;

          return (
            <div
              key={`row-${virtual.index}`}
              className="absolute inset-x-0 grid gap-4 pt-3"
              style={{ ...placement, gridTemplateColumns: `repeat(${perRow}, minmax(0, 1fr))` }}
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
