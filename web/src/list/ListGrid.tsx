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
          // A div, not a button: renderCard may nest a real <a> (a card's own
          // title, made clickable) -- interactive content inside a <button> is
          // invalid HTML and the click gets eaten before the router sees it.
          // role="button" plus onKeyDown keeps the same keyboard activation.
          return (
            <div
              key={id}
              role="button"
              tabIndex={0}
              aria-pressed={selected}
              onClick={(event) => list.toggle(id, event.shiftKey)}
              onKeyDown={(event) => {
                if (event.key === "Enter" || event.key === " ") {
                  event.preventDefault();
                  list.toggle(id, event.shiftKey);
                }
              }}
              className={`absolute inset-x-0 flex cursor-default items-center rounded-md border
                          px-3 text-left text-sm ${
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
            </div>
          );
        })}
      </div>
    </div>
  );
}
