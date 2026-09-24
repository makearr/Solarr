import { useState } from "react";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { attemptLabel, outcomeLabel, passLabel } from "@/lib/identificationEvidence";

import { IdentificationCandidates, type IdentificationCandidateView } from "./IdentificationCandidates";

type IdentificationAttempt = components["schemas"]["IdentificationAttempt"];

/** One queued file's manual decision: every recent identification run, pass by pass, with the
 *  albums each pass weighed and why, so the user chooses from evidence rather than a bare guess. */
export function UnidentifiedReview() {
  const { mediaObjectId: raw } = useParams({ strict: false }) as { mediaObjectId?: string };
  const mediaObjectId = raw !== undefined ? Number(raw) : undefined;
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [wholeFolder, setWholeFolder] = useState(false);
  const [confirmingNotMedia, setConfirmingNotMedia] = useState(false);

  const review = useQuery({
    queryKey: ["unidentified", mediaObjectId, "attempts"],
    enabled: mediaObjectId !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/library/unidentified/{mediaObjectId}/attempts", {
        params: { path: { mediaObjectId: mediaObjectId as number } },
      });
      if (error) throw error;
      return data;
    },
  });

  const choose = useMutation({
    mutationFn: async (candidate: IdentificationCandidateView) => {
      if (candidate.provider === undefined || candidate.external_id === undefined) return;
      const { error } = await api.POST("/library/unidentified/{mediaObjectId}/resolve", {
        params: { path: { mediaObjectId: mediaObjectId as number } },
        body: { provider: candidate.provider, external_id: candidate.external_id, whole_folder: wholeFolder },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["unidentified"] });
      void navigate({ to: "/unidentified" });
    },
  });

  const notMedia = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/library/unidentified/{mediaObjectId}/not-media", {
        params: { path: { mediaObjectId: mediaObjectId as number } },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["unidentified"] });
      void navigate({ to: "/unidentified" });
    },
  });

  if (review.isLoading) return <Message>Loading…</Message>;
  if (review.error !== null || !review.data) {
    return <Message>This file is not waiting for a decision any more.</Message>;
  }

  const [latest, ...older] = review.data.items;
  const anyChoice = latest?.passes.some((pass) =>
    pass.candidates.some((candidate) => candidate.provider === "musicbrainz:release-group"),
  );
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-6xl flex-col gap-5 p-6">
        <header className="flex flex-col gap-1">
          <Link to="/unidentified" className="text-xs text-ink-muted hover:text-ink">
            ← Unidentified
          </Link>
          <h1 className="break-all font-mono text-sm text-ink">{review.data.file.rel_path}</h1>
          <p className="text-xs text-ink-muted">
            Pick the album this file belongs to. Each pass below shows what it looked at and why it
            did not decide on its own.
          </p>
        </header>

        <div className="flex flex-wrap items-center gap-3 text-xs">
          {anyChoice && (
            <label className="flex items-center gap-1.5 text-ink">
              <input type="checkbox" checked={wholeFolder} onChange={(event) => setWholeFolder(event.target.checked)} />
              Use the album I pick for every file waiting in this folder
            </label>
          )}
          <span className="ml-auto flex items-center gap-1.5">
            {confirmingNotMedia ? (
              <>
                <span className="text-ink-muted">Never ask about this file again?</span>
                <button
                  type="button"
                  disabled={notMedia.isPending}
                  onClick={() => notMedia.mutate()}
                  className="rounded-md bg-accent px-2 py-0.5 text-accent-ink disabled:opacity-50"
                >
                  Not media
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmingNotMedia(false)}
                  className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink"
                >
                  Cancel
                </button>
              </>
            ) : (
              <button
                type="button"
                onClick={() => setConfirmingNotMedia(true)}
                title="A cover scan, a booklet, a sample: nothing to identify. The file stays where it is."
                className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink"
              >
                This is not media
              </button>
            )}
          </span>
        </div>

        {choose.isError && <p className="text-xs text-danger">That album could not be applied.</p>}
        {notMedia.isError && <p className="text-xs text-danger">The file could not be marked.</p>}

        {latest === undefined ? (
          <p className="text-xs text-ink-muted">
            No identification run was recorded for this file yet -- it will be after the next scan.
          </p>
        ) : (
          <Attempt attempt={latest} onChoose={(candidate) => choose.mutate(candidate)} busy={choose.isPending} />
        )}

        {older.length > 0 && (
          <details className="rounded-lg border border-border-subtle bg-surface p-3 text-xs">
            <summary className="cursor-pointer text-ink-muted">Earlier runs ({older.length})</summary>
            <div className="mt-3 flex flex-col gap-4">
              {older.map((attempt) => (
                <Attempt key={attempt.attempted_at} attempt={attempt} />
              ))}
            </div>
          </details>
        )}
      </div>
    </div>
  );
}

function Attempt({
  attempt,
  onChoose,
  busy,
}: {
  attempt: IdentificationAttempt;
  onChoose?: (candidate: IdentificationCandidateView) => void;
  busy?: boolean;
}) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="flex items-baseline gap-2 text-xs">
        <span className="font-medium uppercase tracking-wide text-ink-muted">
          {new Date(attempt.attempted_at).toLocaleString()}
        </span>
        <span className="text-ink">{attemptLabel(attempt.outcome)}</span>
      </h2>
      <ol className="flex flex-col gap-2">
        {attempt.passes.map((pass, index) => (
          <li key={`${pass.pass}-${index}`} className="rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-baseline gap-2 text-xs">
              <span className="font-medium text-ink">
                {index + 1}. {passLabel(pass.pass)}
              </span>
              <span className="text-ink-muted">{outcomeLabel(pass.outcome)}</span>
            </div>
            {pass.candidates.length > 0 && (
              <div className="mt-2">
                <IdentificationCandidates candidates={pass.candidates} onChoose={onChoose} busy={busy} />
              </div>
            )}
          </li>
        ))}
      </ol>
    </section>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
