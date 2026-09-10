import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type ImportSession = components["schemas"]["ImportSession"];
type ImportUnit = components["schemas"]["ImportUnit"];
type ImportOrigin = ImportSession["origin"];

const ORIGINS: readonly { id: ImportOrigin; label: string }[] = [
  { id: "manual", label: "Manual" },
  { id: "watched", label: "Watched folder" },
  { id: "adoption", label: "Adopt existing library" },
];

/** todo 4.21's own SSE shape (internal/importing/importjob.ImportProgress),
 *  one "topic: job" event per cluster committed. */
interface ImportProgress {
  session_id: number;
  phase: "walking" | "done" | "cancelled";
  current_unit?: string;
}

/** Live progress, keyed by session id, most recently seen phase/current unit
 *  per session. Kept outside React state until read, so a burst of events for
 *  a session nobody has selected does not re-render the whole screen. */
function useImportProgressStream(
  onSessionUpdated: (sessionId: number, progress: ImportProgress) => void,
) {
  useEffect(() => {
    const source = new EventSource("/api/v1/events?topics=job");
    source.addEventListener("update", (message) => {
      let batch: { events: { entity: string; data?: ImportProgress }[] };
      try {
        batch = JSON.parse(message.data) as typeof batch;
      } catch {
        return;
      }
      for (const event of batch.events) {
        if (!event.entity.startsWith("import_session:") || !event.data) continue;
        onSessionUpdated(event.data.session_id, event.data);
      }
    });
    return () => source.close();
    // onSessionUpdated is a fresh closure every render by design (it closes
    // over selectedSessionId); re-subscribing on every change would thrash
    // the connection, so it is intentionally excluded and read fresh via a ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}

/** ADR-48: point at any path, and the pipeline (WALK/CLASSIFY/GROUP/EVIDENCE/
 *  IDENTIFY/SHAPE/DECIDE) finds and records what it contains. This screen only
 *  inspects and records -- a COMMIT decision here means confidence cleared the
 *  threshold, not that the file has moved; the move engine (ADR-51) does not
 *  exist yet, so there is nothing to commit or revert from this screen either. */
export function Import() {
  const [selectedSessionId, setSelectedSessionId] = useState<number | null>(null);
  const [liveProgress, setLiveProgress] = useState<Record<number, ImportProgress>>({});
  const queryClient = useQueryClient();

  useImportProgressStream((sessionId, progress) => {
    setLiveProgress((held) => ({ ...held, [sessionId]: progress }));
    void queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    if (sessionId === selectedSessionId) {
      void queryClient.invalidateQueries({ queryKey: ["import-units", sessionId] });
    }
  });

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4">
      <div className="flex items-center gap-3">
        <h2 className="text-sm font-medium">Import</h2>
        <span className="text-xs text-ink-muted">
          Point at any path -- a downloaded release, a watched folder, an existing library
        </span>
      </div>

      <InspectForm />

      <div className="flex min-h-0 flex-1 gap-4">
        <SessionList
          selectedId={selectedSessionId}
          onSelect={setSelectedSessionId}
          liveProgress={liveProgress}
        />
        {selectedSessionId !== null && <UnitList sessionId={selectedSessionId} />}
      </div>
    </div>
  );
}

function InspectForm() {
  const queryClient = useQueryClient();
  const [sourcePath, setSourcePath] = useState("");
  const [origin, setOrigin] = useState<ImportOrigin>("manual");

  const inspect = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/import/inspect", {
        body: { source_path: sourcePath, origin },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setSourcePath("");
      queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    },
  });

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        inspect.mutate();
      }}
    >
      <label className="flex min-w-64 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Source path
        <input
          className={inputStyle}
          value={sourcePath}
          placeholder="/downloads/incoming/Artist - Album"
          autoComplete="off"
          onChange={(event) => setSourcePath(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Origin
        <select
          className={inputStyle}
          value={origin}
          onChange={(event) => setOrigin(event.target.value as ImportOrigin)}
        >
          {ORIGINS.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.label}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={!sourcePath.trim() || inspect.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {inspect.isPending ? "Enqueuing…" : "Inspect"}
      </button>
      {inspect.isError && (
        <p className="w-full text-xs text-danger">
          That could not be enqueued -- check the path and that you are an administrator.
        </p>
      )}
    </form>
  );
}

const RUNNING_STATES = new Set<ImportSession["state"]>(["INSPECTING", "CANCELLING"]);

/** Rate from measured throughput, absent while the sample is small (todo
 *  4.21's own wording) -- no total is known ahead of time (units_estimated
 *  is a cheap, tag-free, possibly-undercounting pre-count, not a real
 *  count), so this reads units_committed+queued+skipped+failed against
 *  started_at rather than trusting any single "N of M" framing. */
function describeProgress(session: ImportSession): string | null {
  const processed =
    session.units_committed + session.units_queued + session.units_skipped + session.units_failed;
  const minSampleUnits = 5;
  const minSampleMs = 10_000;
  const elapsedMs = Date.now() - new Date(session.started_at).getTime();
  if (processed < minSampleUnits || elapsedMs < minSampleMs) return null;

  const perMinute = processed / (elapsedMs / 60_000);
  const rate = `~${Math.max(1, Math.round(perMinute))} units/min`;

  const estimated = session.units_estimated;
  if (estimated === undefined || estimated <= processed) return rate;
  const remainingMinutes = (estimated - processed) / perMinute;
  if (!Number.isFinite(remainingMinutes) || remainingMinutes <= 0) return rate;
  const eta =
    remainingMinutes < 1 ? "<1 min left" : `~${Math.round(remainingMinutes)} min left`;
  return `${rate} · ${eta} (of ~${estimated} est.)`;
}

function SessionList({
  selectedId,
  onSelect,
  liveProgress,
}: {
  selectedId: number | null;
  onSelect: (id: number) => void;
  liveProgress: Record<number, ImportProgress>;
}) {
  const queryClient = useQueryClient();
  const sessions = useQuery({
    queryKey: ["import-sessions"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/sessions");
      if (error) throw error;
      return data;
    },
    refetchInterval: 5000,
  });

  const cancel = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.POST("/import/sessions/{id}/cancel", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["import-sessions"] }),
  });

  return (
    <div className="flex w-96 shrink-0 flex-col gap-2 overflow-y-auto">
      {sessions.isLoading && <Message>Loading sessions…</Message>}
      {sessions.error !== null && <Message>Sessions could not be read.</Message>}
      {sessions.data && sessions.data.length === 0 && (
        <Message>No import has run yet.</Message>
      )}
      {sessions.data?.map((session) => (
        <div
          key={session.id}
          className={`flex flex-col gap-1 rounded-lg border p-3 text-left text-xs ${
            selectedId === session.id
              ? "border-accent bg-surface"
              : "border-border-subtle bg-surface hover:border-accent/50"
          }`}
        >
          <div className="flex items-start gap-2">
            <button
              type="button"
              onClick={() => onSelect(session.id)}
              className="flex min-w-0 flex-1 flex-col gap-1 text-left focus-visible:outline-2
                         focus-visible:outline-offset-2 focus-visible:outline-accent"
            >
              <span className="truncate font-mono text-sm text-ink" title={session.source_path}>
                {session.source_path}
              </span>
              <span className="flex flex-wrap items-center gap-1.5">
                <StateBadge state={session.state} />
                <span className="text-ink-muted">{session.origin}</span>
              </span>
              <span className="text-ink-muted">
                {session.units_committed} committed / {session.units_queued} queued /{" "}
                {session.units_skipped} skipped / {session.units_failed} failed /{" "}
                {session.units_total} total
              </span>
              {RUNNING_STATES.has(session.state) && liveProgress[session.id]?.current_unit && (
                <span className="truncate text-ink-muted" title={liveProgress[session.id]?.current_unit}>
                  now: {liveProgress[session.id]?.current_unit}
                </span>
              )}
              {RUNNING_STATES.has(session.state) && describeProgress(session) && (
                <span className="text-ink-muted">{describeProgress(session)}</span>
              )}
              {session.error && <span className="text-danger">{session.error}</span>}
            </button>
            {RUNNING_STATES.has(session.state) && (
              <button
                type="button"
                onClick={() => cancel.mutate(session.id)}
                disabled={cancel.isPending}
                title="Cancel this import"
                className="shrink-0 rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-50
                           focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
              >
                <Ban aria-hidden className="size-3.5" />
              </button>
            )}
          </div>
        </div>
      ))}
    </div>
  );
}

function UnitList({ sessionId }: { sessionId: number }) {
  const units = useQuery({
    queryKey: ["import-units", sessionId],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/sessions/{id}/units", {
        params: { path: { id: sessionId }, query: { limit: 200 } },
      });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="min-h-0 flex-1 overflow-y-auto rounded-lg border border-border-subtle bg-surface">
      {units.isLoading && <Message>Loading units…</Message>}
      {units.error !== null && <Message>Units could not be read.</Message>}
      {units.data && units.data.length === 0 && <Message>Nothing found under this path.</Message>}
      {units.data && units.data.length > 0 && (
        <ul className="divide-y divide-border-subtle">
          {units.data.map((unit) => (
            <UnitRow key={unit.id} unit={unit} />
          ))}
        </ul>
      )}
    </div>
  );
}

function UnitRow({ unit }: { unit: ImportUnit }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <li className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
      <button
        type="button"
        onClick={() => setExpanded((value) => !value)}
        className="flex flex-wrap items-center gap-2 text-left focus-visible:outline-2
                   focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <span className="font-mono text-sm text-ink">{unit.rel_path}</span>
        <ShapeBadge shape={unit.shape} />
        <DecisionBadge decision={unit.decision} />
        <span className="text-ink-muted">
          {unit.file_count} files, {Math.round(unit.confidence * 100)}% confidence
        </span>
      </button>
      {expanded && (
        <div className="flex flex-col gap-1 rounded-md bg-canvas p-2 text-ink-muted">
          {unit.evidence?.signals && unit.evidence.signals.length > 0 ? (
            <ul className="flex flex-col gap-0.5">
              {unit.evidence.signals.map((signal, index) => (
                <li key={index}>
                  <span className="font-medium text-ink">{signal.kind}</span>
                  {signal.detail && `: ${signal.detail}`}
                </li>
              ))}
            </ul>
          ) : (
            <span>No evidence recorded.</span>
          )}
          {unit.candidates && unit.candidates.length > 0 && (
            <ul className="flex flex-col gap-0.5 border-t border-border-subtle pt-1">
              {unit.candidates.map((candidate, index) => (
                <li key={index}>
                  {candidate.strategy}
                  {candidate.provider && ` -- ${candidate.provider}:${candidate.external_id}`}
                  {` (${Math.round(candidate.confidence * 100)}%)`}
                </li>
              ))}
            </ul>
          )}
          {unit.reason && <span>Reason: {unit.reason}</span>}
        </div>
      )}
    </li>
  );
}

function StateBadge({ state }: { state: ImportSession["state"] }) {
  const isTerminal = state === "DONE" || state === "FAILED" || state === "REVERTED" || state === "CANCELLED";
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "FAILED"
          ? "border-danger text-danger"
          : isTerminal
            ? "border-border-subtle text-ink-muted"
            : "border-accent text-accent"
      }`}
    >
      {state}
    </span>
  );
}

function ShapeBadge({ shape }: { shape: ImportUnit["shape"] }) {
  return (
    <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
      {shape}
    </span>
  );
}

function DecisionBadge({ decision }: { decision: ImportUnit["decision"] }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        decision === "COMMIT"
          ? "border-accent text-accent"
          : decision === "FAILED"
            ? "border-danger text-danger"
            : "border-border-subtle text-ink-muted"
      }`}
    >
      {decision}
    </span>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-3 text-xs text-ink-muted">{children}</p>;
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
