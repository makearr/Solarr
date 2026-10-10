import { type ReactNode, useEffect, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type LibraryRenamePlan = components["schemas"]["LibraryRenamePlan"];

/** A preview of every file of a library, or of one of its works, whose name would change, then the rename. */
export function RenamePlanner({
  title,
  categoryId,
  workId,
  unsaved = false,
  previewAtOnce = false,
}: {
  title?: ReactNode;
  categoryId: number;
  workId?: number;
  unsaved?: boolean;
  previewAtOnce?: boolean;
}) {
  const [plan, setPlan] = useState<LibraryRenamePlan | undefined>();
  const run = useMutation({
    mutationFn: async (preview: boolean) => {
      const { data, error } = await api.POST("/categories/{id}/rename", {
        params: { path: { id: categoryId } },
        body: { preview, work_id: workId },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => setPlan(data),
  });

  const { mutate } = run;
  useEffect(() => {
    if (previewAtOnce) mutate(true);
  }, [previewAtOnce, mutate]);

  const moves = plan?.roots.flatMap((root) => root.plan.moves) ?? [];
  const refused = plan?.roots.flatMap((root) => root.plan.refused) ?? [];
  const started = plan?.roots.some((root) => root.plan.accepted) ?? false;

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        {title}
        <button
          type="button"
          disabled={unsaved || run.isPending}
          title={unsaved ? "Save the naming first" : "Show every file whose name would change"}
          onClick={() => run.mutate(true)}
          className="ml-auto rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink disabled:opacity-50"
        >
          {run.isPending && run.variables ? "Checking…" : "Preview renames"}
        </button>
      </div>
      {unsaved && <p className="text-xs text-ink-muted">Save the naming first; the library is renamed with what is saved.</p>}
      {run.isError && <p className="text-xs text-danger">{(run.error as { detail?: string }).detail ?? "The rename could not be planned."}</p>}

      {plan && started && (
        <p className="text-sm">
          Renaming {moves.length} {moves.length === 1 ? "file" : "files"}. Progress is under{" "}
          <Link to="/storage/roots" className="text-accent hover:underline">
            Storage → Root folders
          </Link>
          .
        </p>
      )}
      {plan && !started && moves.length === 0 && refused.length === 0 && (
        <p className="text-sm text-ink-muted">Every file already has the name this library would give it.</p>
      )}
      {plan && !started && moves.length > 0 && (
        <>
          <p className="text-sm">
            {moves.length} {moves.length === 1 ? "file" : "files"} would be renamed.
          </p>
          <ul className="max-h-80 overflow-y-auto rounded-md border border-border-subtle text-xs">
            {moves.map((move) => (
              <li key={move.media_object_id} className="flex flex-col border-b border-border-subtle px-2 py-1 last:border-0">
                <span className="break-all font-mono text-ink-muted line-through">{move.source_rel_path}</span>
                <span className="break-all font-mono">{move.dest_rel_path}</span>
              </li>
            ))}
          </ul>
          <div>
            <button
              type="button"
              disabled={run.isPending}
              onClick={() => run.mutate(false)}
              className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
            >
              {run.isPending && !run.variables ? "Starting…" : `Rename ${moves.length} ${moves.length === 1 ? "file" : "files"}`}
            </button>
          </div>
        </>
      )}
      {plan && refused.length > 0 && (
        <p className="text-xs text-warn">
          {refused.length} {refused.length === 1 ? "file is" : "files are"} left alone: {refused[0]?.reason}
        </p>
      )}
    </div>
  );
}
