import type { components } from "@/api/schema";
import { targetClosenessChip } from "@/lib/targetCloseness";
import { Chip } from "@/ui/Chip";

type PartFile = components["schemas"]["PartFile"];

/** How near a file is to its library's target quality; the plain yes or no when nothing could be compared. */
export function TargetChip({ file, short = false }: { file: PartFile; short?: boolean }) {
  const closeness = targetClosenessChip(file);
  if (closeness) {
    return (
      <Chip tone={closeness.tone} title={closeness.title}>
        {short ? `${file.target_closeness_pct}%` : closeness.text}
      </Chip>
    );
  }
  if (file.meets_target == null) return null;
  return <Chip tone={file.meets_target ? "ok" : "warn"}>{short ? "target" : file.meets_target ? "meets the target" : "below the target"}</Chip>;
}
