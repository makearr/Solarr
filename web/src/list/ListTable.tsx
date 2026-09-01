import type { LibraryList } from "./useLibraryList";
import type { Id } from "./selection";

export interface Column<T> {
  readonly key: string;
  readonly header: string;
  readonly render: (item: T) => React.ReactNode;
  /** Absent means the column cannot be sorted, which is the default. */
  readonly sortable?: boolean;
  readonly width?: string;
}

/**
 * The dense view. Presentational only (section 21.2): it decides how a row
 * looks and nothing else. Every behaviour -- what is selected, what is sorted,
 * what is loaded -- belongs to the hook, so adding a column or a bulk action
 * does not have to be done twice.
 */
export function ListTable<T>({
  list,
  columns,
  idOf,
}: {
  list: LibraryList<T>;
  columns: readonly Column<T>[];
  idOf: (item: T) => Id;
}) {
  const template = columns.map((column) => column.width ?? "1fr").join(" ");

  return (
    <div className="flex h-full flex-col">
      <div
        role="row"
        className="grid shrink-0 gap-3 border-b border-border-subtle bg-surface px-3 py-2
                   text-xs font-medium text-ink-muted"
        style={{ gridTemplateColumns: `2rem ${template}` }}
      >
        <span />
        {columns.map((column) => (
          <button
            key={column.key}
            type="button"
            disabled={!column.sortable}
            onClick={() => list.sortBy(column.key)}
            className="flex items-center gap-1 text-left enabled:hover:text-ink
                       disabled:cursor-default"
          >
            {column.header}
            {list.query.sort === column.key && (
              <span aria-hidden>{list.query.direction === "asc" ? "▲" : "▼"}</span>
            )}
          </button>
        ))}
      </div>

      <div ref={list.scrollRef} className="min-h-0 flex-1 overflow-auto">
        {/* The spacer carries the full height of the collection so the
            scrollbar tells the truth about 500k rows, while only the window
            below is ever in the document. */}
        <div style={{ height: list.totalSize, position: "relative" }}>
          {list.virtualItems.map((virtual) => {
            const item = list.items[virtual.index];
            if (!item) return <LoadingRow key="more" virtual={virtual} />;

            const id = idOf(item);
            const selected = list.isSelected(id);
            return (
              <div
                key={id}
                role="row"
                aria-selected={selected}
                onClick={(event) => list.toggle(id, event.shiftKey)}
                className={`absolute inset-x-0 grid cursor-default items-center gap-3 border-b
                            border-border-subtle/60 px-3 text-sm
                            ${selected ? "bg-accent/10" : "hover:bg-surface"}`}
                style={{
                  height: virtual.size,
                  transform: `translateY(${virtual.start}px)`,
                  gridTemplateColumns: `2rem ${template}`,
                }}
              >
                <input
                  type="checkbox"
                  checked={selected}
                  aria-label={`Select row ${id}`}
                  onChange={() => list.toggle(id)}
                  onClick={(event) => event.stopPropagation()}
                />
                {columns.map((column) => (
                  <span key={column.key} className="truncate">
                    {column.render(item)}
                  </span>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

function LoadingRow({ virtual }: { virtual: { size: number; start: number } }) {
  return (
    <div
      className="absolute inset-x-0 px-3 text-sm text-ink-muted"
      style={{ height: virtual.size, transform: `translateY(${virtual.start}px)` }}
    >
      Loading more…
    </div>
  );
}
