import type { components } from "@/api/schema";

type IndexerSearchError = components["schemas"]["IndexerSearchError"];

/** Names each indexer a search went without, so a missing release is not a mystery (ADR-37). */
export function IndexerProblems({ problems }: { problems: IndexerSearchError[] | undefined }) {
  return (
    <>
      {(problems ?? []).map((problem) => (
        <p key={problem.indexer_id} className="text-xs text-warn">
          {problem.too_slow
            ? `${problem.indexer_name} did not answer within 20 seconds; its releases are missing.`
            : `${problem.indexer_name} failed: ${shortReason(problem.error)}`}
        </p>
      ))}
    </>
  );
}

/** The last two links of a Go error chain -- "connect: no route to host" -- not the whole URL. */
function shortReason(error: string): string {
  return error.split(": ").slice(-2).join(": ");
}
