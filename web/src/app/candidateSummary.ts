import type { components } from "@/api/schema";

type CandidateDecision = components["schemas"]["CandidateDecision"];

/** F1: the latest search in one sentence, so "why was nothing downloaded" needs no reading of every release. */
export function candidateSummary(decisions: readonly CandidateDecision[]): string {
  if (decisions.length === 0) return "";
  const usable = decisions.filter((decision) => decision.outcome === "ACCEPT" && !decision.blocklisted);
  const deferred = decisions.filter((decision) => decision.outcome === "DEFER");
  const rejected = decisions.filter((decision) => decision.outcome === "REJECT");
  const parts = [`${decisions.length} ${decisions.length === 1 ? "release" : "releases"} found`];
  if (usable.length > 0) {
    parts.push(`${usable.length} acceptable, the best is ${usable[0]?.title ?? ""}`);
  } else if (rejected.length === decisions.length) {
    parts.push("none acceptable");
  }
  if (deferred.length > 0) parts.push(`${deferred.length} deferred (${topReasons(deferred.flatMap((decision) => decision.deferrals.map((deferral) => deferral.reason)))})`);
  if (rejected.length > 0) parts.push(`${rejected.length} rejected (${topReasons(rejected.flatMap((decision) => decision.rejections.map((rejection) => rejection.field)))})`);
  return `${parts.join("; ")}.`;
}

/** The three most common reasons with their counts; a reason counts once per release it is raised on. */
function topReasons(reasons: readonly string[]): string {
  const counts = new Map<string, number>();
  for (const reason of reasons) counts.set(reason, (counts.get(reason) ?? 0) + 1);
  return [...counts]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, 3)
    .map(([reason, count]) => `${reason} ×${count}`)
    .join(", ");
}
