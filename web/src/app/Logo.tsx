/** The mark from the browser tab, beside the name wherever the name is shown. */
export function Logo({ showName = true, size = "md" }: { showName?: boolean; size?: "md" | "lg" }) {
  return (
    <span className="flex items-center gap-2.5">
      <img src="/favicon.svg" alt="" className={`${size === "lg" ? "size-9" : "size-7"} shrink-0`} />
      {showName && (
        <span className={`${size === "lg" ? "text-lg" : "text-base"} font-semibold tracking-tight`}>Solarr</span>
      )}
    </span>
  );
}
