import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

type Decision = components["schemas"]["ImportDecisionRecord"];
type Side = components["schemas"]["ReplacementSide"];

/** Pain point 18: each time a download and an owned file contested a part, the two side by side and the tier that decided. */
export function ReplacementHistory({ workId }: { workId: number }) {
  const decisions = useQuery({
    queryKey: ["work", workId, "import-decisions"],
    queryFn: () => ok(api.GET("/works/{id}/import-decisions", { params: { path: { id: workId } } })),
  });
  if (!decisions.data || decisions.data.length === 0) return null;
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-ink-muted hover:text-ink">Why files were replaced or kept ({decisions.data.length})</summary>
      <ul className="mt-2 flex flex-col gap-2">
        {decisions.data.map((decision) => (
          <li key={decision.id} className="rounded-lg border border-border-subtle bg-surface p-3">
            <p className="mb-1">
              <span className={decision.outcome === "IMPORT" ? "text-ok" : "text-warn"}>{headline(decision)}</span>{" "}
              <span className="text-ink-muted">{new Date(decision.decided_at).toLocaleString()}</span>
            </p>
            <table className="w-full text-left">
              <thead className="text-ink-muted">
                <tr>
                  <th className="pr-3 font-normal">Quality</th>
                  <th className="pr-3 font-normal">Arrived</th>
                  <th className="font-normal">Owned</th>
                </tr>
              </thead>
              <tbody>
                {rows(decision.arrived, decision.incumbent).map((row) => (
                  <tr key={row.dimension} className={row.dimension === decision.decided_by ? "font-medium" : ""}>
                    <td className="pr-3 text-ink-muted">{row.dimension}</td>
                    <td className="pr-3">{row.arrived}</td>
                    <td>{row.incumbent}</td>
                  </tr>
                ))}
                <tr className={decision.decided_by === "score" ? "font-medium" : ""}>
                  <td className="pr-3 text-ink-muted">score</td>
                  <td className="pr-3">{decision.arrived.score.toFixed(1)}</td>
                  <td>{decision.incumbent.score.toFixed(1)}</td>
                </tr>
              </tbody>
            </table>
          </li>
        ))}
      </ul>
    </details>
  );
}

/** One sentence: the outcome and what decided it; a tie on every tier keeps the owned file. */
export function headline(decision: Pick<Decision, "outcome" | "decided_by">): string {
  const by = decision.decided_by === "score" ? "the tiers tied and the score decided" : `decided by ${decision.decided_by}`;
  return decision.outcome === "IMPORT" ? `The download replaced the owned file: ${by}.` : `The owned file was kept: ${by}; a tie keeps what is owned.`;
}

/** The two files' tiers aligned by dimension, in the profile's order. */
export function rows(arrived: Side, incumbent: Side): { dimension: string; arrived: string; incumbent: string }[] {
  const label = (side: Side, dimension: string) => side.tiers.find((tier) => tier.dimension === dimension)?.label ?? "–";
  return arrived.tiers.map((tier) => ({ dimension: tier.dimension, arrived: tier.label, incumbent: label(incumbent, tier.dimension) }));
}
