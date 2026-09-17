import {
  editionSummary,
  factsOf,
  recallLabel,
  verdictOf,
  type EvidenceFact,
} from "@/lib/identificationEvidence";

export interface IdentificationCandidateView {
  strategy: string;
  provider?: string;
  external_id?: string;
  confidence: number;
  evidence?: readonly EvidenceFact[];
}

const RELEASE_GROUP = "musicbrainz:release-group";

/** The albums an identification weighed, each with the facts its verdict rests on. With
 *  onChoose, a row the user may pick carries a button; choosing is the manual decision. */
export function IdentificationCandidates({
  candidates,
  onChoose,
  busy = false,
}: {
  candidates: readonly IdentificationCandidateView[];
  onChoose?: (candidate: IdentificationCandidateView) => void;
  busy?: boolean;
}) {
  if (candidates.length === 0) return null;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-xs">
        <thead className="text-ink-muted">
          <tr className="border-b border-border-subtle">
            <th className="px-2 py-1 font-medium">Album</th>
            <th className="px-2 py-1 font-medium">Artist</th>
            <th className="px-2 py-1 font-medium">Edition</th>
            <th className="px-2 py-1 font-medium">Files matched</th>
            <th className="px-2 py-1 font-medium">Verdict</th>
            <th className="px-2 py-1 font-medium">Found by</th>
            {onChoose && <th className="px-2 py-1" />}
          </tr>
        </thead>
        <tbody>
          {candidates.map((candidate, index) => {
            const facts = factsOf(candidate.evidence);
            const isReleaseGroup = candidate.provider === RELEASE_GROUP && candidate.external_id !== undefined;
            return (
              <tr key={`${candidate.external_id ?? candidate.strategy}-${index}`} className="border-b border-border-subtle align-top">
                <td className="px-2 py-1.5">
                  <div className="text-ink">
                    {isReleaseGroup ? (
                      <a
                        href={`https://musicbrainz.org/release-group/${candidate.external_id}`}
                        target="_blank"
                        rel="noreferrer"
                        className="hover:underline"
                      >
                        {facts.title || candidate.external_id}
                      </a>
                    ) : (
                      (facts.title ?? candidate.external_id ?? candidate.strategy)
                    )}
                  </div>
                  {facts.release_type && <div className="text-ink-muted">{facts.release_type}</div>}
                </td>
                <td className="px-2 py-1.5">{facts.artist}</td>
                <td className="px-2 py-1.5 text-ink-muted">{editionSummary(facts)}</td>
                <td className="px-2 py-1.5">
                  {facts.files_matched ?? `${Math.round(candidate.confidence * 100)}% confidence`}
                  {facts.files_supported && (
                    <div className="text-ink-muted">{facts.files_supported} by title or recording</div>
                  )}
                </td>
                <td className="px-2 py-1.5">
                  {verdictOf(facts)}
                  {facts.distance && <div className="text-ink-muted">distance {facts.distance}</div>}
                </td>
                <td className="px-2 py-1.5 text-ink-muted">{recallLabel(facts.recall)}</td>
                {onChoose && (
                  <td className="px-2 py-1.5 text-right">
                    {isReleaseGroup && (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => onChoose(candidate)}
                        className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink
                                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                                   focus-visible:outline-accent"
                      >
                        Use this album
                      </button>
                    )}
                  </td>
                )}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
