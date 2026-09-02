/** The library list lands at stage 3, when there is something to list. Until
 *  then this says so rather than rendering an empty table, which reads as a
 *  bug. The list core it will use is already built and tested: see
 *  web/src/list/useLibraryList.ts. */
export function Library() {
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="flex max-w-md flex-col gap-2 text-center">
        <h1 className="text-sm font-medium">Nothing in the library yet</h1>
        <p className="text-xs text-ink-muted">
          Identification and import arrive at stage 3. The table and grid views, their filters
          and the selection model are already built and exercised by Activity.
        </p>
      </div>
    </div>
  );
}
