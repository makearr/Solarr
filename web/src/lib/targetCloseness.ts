import type { components } from "@/api/schema";
import type { ChipTone } from "@/ui/Chip";

type PartFile = components["schemas"]["PartFile"];

/** How far off the target a size is, the same for a factor and its inverse: half and double are both 0.5. */
export function sizeOff(pct: number): number {
  const ratio = pct / 100;
  if (ratio <= 0) return 1;
  return 1 - (ratio > 1 ? 1 / ratio : ratio);
}

/** Green within 10%, yellow to 25%, orange to 50%, red from there. */
export function sizeTone(pct: number): ChipTone {
  const off = sizeOff(pct);
  if (off < 0.1) return "ok";
  if (off < 0.25) return "caution";
  if (off < 0.5) return "orange";
  return "danger";
}

/** "94%, below the target", "108% above the target", or "100% of the target" while it is near enough. */
export function sizeText(pct: number): string {
  if (sizeOff(pct) < 0.1) return `${pct}% of the target`;
  return pct < 100 ? `${pct}%, below the target` : `${pct}% above the target`;
}

const megabytes = (kbps: number, perMinutes: number) => (kbps * 60 * perMinutes) / 8 / 1000;

/** The chip for how a file's size stands against its library's target, and what a hover lists as different. */
export function targetClosenessChip(file: PartFile): { text: string; title: string; tone: ChipTone } | null {
  if (file.target_size_pct == null) return null;
  const lines: string[] = [];
  if (file.target_expected_kbps) {
    const perMinute = megabytes(file.target_expected_kbps, 1);
    lines.push(`A file on the target is about ${perMinute.toFixed(1)} MB/min (${Math.round(perMinute * 60)} MB/h, ${file.target_expected_kbps} kbit/s)`);
  }
  const gaps = (file.target_gaps ?? []).map((gap) => `${gap.dimension}: ${gap.have} (wanted ${gap.want})`);
  if (gaps.length > 0) lines.push("Differs from the target in", ...gaps);
  return { text: sizeText(file.target_size_pct), title: lines.join("\n"), tone: sizeTone(file.target_size_pct) };
}
