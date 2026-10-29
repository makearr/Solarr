import type { components } from "@/api/schema";

type ImportList = components["schemas"]["ImportList"];
type Verdict = components["schemas"]["ImportListPreviewEntry"]["verdict"];

/** Two lists' settings say the same once blank values are left out. */
export function sameOptions(a: Record<string, string>, b: Record<string, string>): boolean {
  const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
  return [...keys].every((key) => (a[key] ?? "").trim() === (b[key] ?? "").trim());
}

/** An edit keeps an enabled list enabled only when its type and settings stay as previewed (ADR-38); a new list and
 *  new settings are saved disabled and previewed. */
export function keepsEnabled(list: ImportList | undefined, type: string, options: Record<string, string>): boolean {
  return list !== undefined && list.enabled && list.type === type && sameOptions(list.options, options);
}

/** The comma-separated choices with key ticked or unticked, in the order the field offers them. */
export function toggleChoice(offered: string[], picked: string[], key: string): string {
  return offered.filter((candidate) => (candidate === key) !== picked.includes(candidate)).join(",");
}

/** The order a preview lists verdicts in: what would be added first, then what waits, then what is left out. */
export const VERDICT_ORDER: Verdict[] = ["add", "unchecked", "held", "handled", "excluded", "filtered", "foreign", "unresolved"];
