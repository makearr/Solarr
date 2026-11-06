import type { components } from "@/api/schema";

type PartFile = components["schemas"]["PartFile"];

/** The chip text for how near a file is to its library's target, and what a hover lists as missing. */
export function targetClosenessChip(file: PartFile): { text: string; title: string; tone: "ok" | "warn" } | null {
  if (file.target_closeness_pct == null) return null;
  const gaps = (file.target_gaps ?? []).map((gap) => `${gap.dimension}: ${gap.have} (wanted ${gap.want})`);
  return {
    text: `${file.target_closeness_pct}% of the target`,
    title: gaps.length > 0 ? `Differs from the target in\n${gaps.join("\n")}` : "Matches every part of the target",
    tone: file.target_closeness_pct >= 100 ? "ok" : "warn",
  };
}
