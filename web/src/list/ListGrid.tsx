import type { LibraryList } from "./useLibraryList";
import type { Id } from "./selection";

/** The cover view. Shares every behaviour with ListTable through the hook. */
export function ListGrid<T>({
  list,
  idOf,
  renderCard,
}: {
  list: LibraryList<T>;
  idOf: (item: T) => Id;
  renderCard: (item: T) => React.ReactNode;
}) {
  return (
    <div ref={list.scrollRef} className="h-full overflow-auto p-3">
      <div style={{ height: list.totalSize, position: "relative" }}>
        {list.virtualItems.map((virtual) => {
          const item = list.items[virtual.index];
          if (!item) return null;

          const id = idOf(item);
          const selected = list.isSelected(id);
          return (
            <button
              key={id}
              type="button"
              aria-pressed={selected}
              onClick={(event) => list.toggle(id, event.shiftKey)}
              className={`absolute inset-x-0 flex items-center rounded-md border px-3 text-left
                          text-sm ${
                            selected
                              ? "border-accent bg-accent/10"
                              : "border-border-subtle bg-surface"
                          }`}
              style={{
                height: virtual.size - 6,
                transform: `translateY(${virtual.start}px)`,
              }}
            >
              {renderCard(item)}
            </button>
          );
        })}
      </div>
    </div>
  );
}
