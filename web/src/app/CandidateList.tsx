import type { components } from "@/api/schema";

type CandidateDecision = components["schemas"]["CandidateDecision"];

/** The latest search's releases, best first, each sendable: the album page and Wanted's
 *  interactive search (F16). */
export function CandidateList({
  decisions,
  loading,
  onSend,
  sendPendingId,
  sendError,
  onBlocklist,
}: {
  decisions: CandidateDecision[];
  loading: boolean;
  onSend: (decisionId: number) => void;
  sendPendingId: number | undefined;
  sendError: number | undefined;
  onBlocklist?: (decisionId: number) => void;
}) {
  if (loading) return <p className="text-xs text-ink-muted">Loading…</p>;
  if (decisions.length === 0) {
    return <p className="text-xs text-ink-muted">Nothing found yet -- try Search now.</p>;
  }
  return (
    <ul className="flex flex-col gap-2">
      {decisions.map((decision) => (
        <li
          key={decision.id}
          className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3 text-sm"
        >
          <div className="flex flex-wrap items-center gap-2">
            <OutcomeBadge outcome={decision.outcome} />
            <span className="truncate font-mono text-xs" title={decision.title}>
              {decision.title}
            </span>
            {decision.blocklisted && (
              <span className="rounded-full border border-danger px-1.5 py-px text-[0.6875rem] text-danger">
                blocklisted
              </span>
            )}
            <QualityVerdict decision={decision} />
            {onBlocklist && !decision.blocklisted && (
              <button
                type="button"
                onClick={() => onBlocklist(decision.id)}
                title="Never offer this release again"
                className="shrink-0 rounded-md border border-border-subtle px-2 py-0.5 text-xs
                           text-ink-muted hover:text-danger"
              >
                Blocklist
              </button>
            )}
            {decision.outcome === "ACCEPT" && !decision.blocklisted && (
              <button
                type="button"
                onClick={() => onSend(decision.id)}
                disabled={sendPendingId === decision.id}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-0.5 text-xs
                           text-ink-muted hover:text-ink disabled:opacity-50"
              >
                {sendPendingId === decision.id ? "Sending…" : "Send"}
              </button>
            )}
          </div>
          {sendError === decision.id && (
            <p className="text-xs text-danger">
              That could not be sent -- check that a matching download client is connected.
            </p>
          )}
          <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-ink-muted">
            {decision.indexer_name && <span className="text-ink">{decision.indexer_name}</span>}
            <span>{decision.protocol}</span>
            {decision.size_bytes != null && <span>{formatBytes(decision.size_bytes)}</span>}
            {decision.seeders != null && <span>{decision.seeders} seeders</span>}
            {decision.match_confidence != null && (
              <span>{Math.round(decision.match_confidence * 100)}% match</span>
            )}
            <span>{new Date(decision.decided_at).toLocaleString()}</span>
          </div>
          {decision.rejections.length > 0 && (
            <ul className="text-xs text-danger">
              {decision.rejections.map((rejection, index) => (
                <li key={index}>
                  {rejection.field}: wanted {rejection.wanted}, got {rejection.got}
                </li>
              ))}
            </ul>
          )}
          {decision.deferrals.length > 0 && (
            <ul className="text-xs text-warn">
              {decision.deferrals.map((deferral, index) => (
                <li key={index}>{deferral.reason}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}

// Releases rank by the library's profiles first (ADR-40 tiers); the additive score only breaks ties,
// and is zero unless a profile scores, so the profile a release fits is what is worth reading.
function QualityVerdict({ decision }: { decision: CandidateDecision }) {
  const fit = decision.components.find((component) => component.rule === "profile-priority");
  const fits = fit !== undefined && fit.label.endsWith("(fits)");
  return (
    <span className={`ml-auto shrink-0 text-xs ${fits ? "text-ok" : "text-ink-muted"}`}>
      {fit?.label}
      {decision.score !== 0 && ` · score ${decision.score.toFixed(1)}`}
    </span>
  );
}

function OutcomeBadge({ outcome }: { outcome: CandidateDecision["outcome"] }) {
  const tone =
    outcome === "ACCEPT" ? "text-ok border-border-subtle" : outcome === "DEFER" ? "text-warn border-warn" : "text-danger border-danger";
  return (
    <span className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${tone}`}>{outcome}</span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

