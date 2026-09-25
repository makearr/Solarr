/** The mark from the browser tab, beside the name wherever the name is shown. */
export function Logo({ showName = true }: { showName?: boolean }) {
  return (
    <span className="flex items-center gap-2">
      <img src="/favicon.svg" alt="" className="size-6 shrink-0" />
      {showName && <span className="text-sm font-semibold tracking-tight">Solarr</span>}
    </span>
  );
}
