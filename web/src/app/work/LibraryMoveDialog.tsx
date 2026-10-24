import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { invalidateWorkLists, useLibraries } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";

import { Companions } from "../Moves";
import { formatBytes } from "../Queue";

type LibraryMovePlan = components["schemas"]["LibraryMovePlan"];
type BulkLibraryMove = components["schemas"]["BulkLibraryMove"];

/** What goes: one work, or a list's selection as the bulk endpoints take it. */
export type MoveSubject = { workId: number; title: string } | { selection: Record<string, unknown>; count: number };

/** Move or copy to another library of the same media type: pick it, see what moves where, then start. A move takes the
 *  work along; a copy places the files again in the other library, unmonitored there. */
export function LibraryMoveDialog({
  subject, mediaType, fromLibraryId, initialTarget = 0, onClose, onDone,
}: { subject: MoveSubject; mediaType: string; fromLibraryId: number; initialTarget?: number; onClose: () => void; onDone: () => void }) {
  const queryClient = useQueryClient();
  const targets = (useLibraries().data ?? []).filter((library) => library.media_type === mediaType && library.id !== fromLibraryId);
  const [target, setTarget] = useState(initialTarget);
  const [copy, setCopy] = useState(false);
  const chosen = target || targets[0]?.id || 0;
  const one = "workId" in subject;

  const plan = useMutation({
    mutationFn: async (preview: boolean): Promise<LibraryMovePlan | BulkLibraryMove> => {
      if ("workId" in subject) {
        return ok(api.POST("/works/{id}/library", {
          params: { path: { id: subject.workId } },
          body: { category_id: chosen, from_category_id: fromLibraryId, copy, preview },
        }));
      }
      return ok(api.POST("/works/bulk/library", { body: { ...subject.selection, library_id: fromLibraryId, category_id: chosen, copy, preview } }));
    },
    onSuccess: (_, preview) => {
      if (preview) return;
      invalidateWorkLists(queryClient);
      void queryClient.invalidateQueries({ queryKey: ["work"] });
      void queryClient.invalidateQueries({ queryKey: ["move-jobs"] });
      onDone();
    },
  });
  const shown = plan.data && plan.variables ? plan.data : undefined;
  const single = shown && "plan" in shown ? shown : undefined;
  const bulk = shown && "works" in shown ? shown : undefined;
  const canStart = single ? !single.refusal && single.plan.refused.length === 0 : bulk ? bulk.works > 0 : false;
  const verb = copy ? "Copy" : "Move";

  return (
    <Dialog
      title={one ? `${verb} to another library` : `${verb} ${subject.count} to another library`}
      description={one ? subject.title : undefined}
      width="wide"
      onClose={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={!chosen} busy={plan.isPending && plan.variables} onClick={() => plan.mutate(true)}>
            Preview
          </Button>
          <Button variant="primary" disabled={!canStart} busy={plan.isPending && !plan.variables} onClick={() => plan.mutate(false)}>
            {verb}
          </Button>
        </>
      }
    >
      {targets.length === 0 ? (
        <p className="text-sm text-ink-muted">There is no other library of this kind to go to.</p>
      ) : (
        <div className="flex flex-col gap-3 text-sm">
          <div className="flex flex-wrap items-center gap-3">
            <label className="flex items-center gap-2">
              To
              <select
                value={chosen}
                onChange={(event) => { setTarget(Number(event.target.value)); plan.reset(); }}
                className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5"
              >
                {targets.map((library) => (
                  <option key={library.id} value={library.id}>
                    {library.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex items-center gap-1.5">
              <input type="radio" checked={!copy} onChange={() => { setCopy(false); plan.reset(); }} />
              Move
            </label>
            <label className="flex items-center gap-1.5" title="The files are placed again in the other library (reflink or hardlink where the disk allows); it is unmonitored there.">
              <input type="radio" checked={copy} onChange={() => { setCopy(true); plan.reset(); }} />
              Copy
            </label>
          </div>
          {plan.isError && <p className="text-danger">That could not be planned.</p>}
          {single?.refusal && <p className="text-danger">It cannot go there: {single.refusal}.</p>}
          {single && !single.refusal && (
            <>
              <p className="text-ink-muted">
                {single.plan.files_total === 0
                  ? "No files to move; the work alone changes library."
                  : `${single.plan.files_total} ${single.plan.files_total === 1 ? "file" : "files"}, ${formatBytes(single.plan.bytes_total)}, named by the other library's naming.`}
                {single.plan.refused[0] && <span className="text-danger"> Refused: {single.plan.refused[0].reason}</span>}
              </p>
              <ul className="max-h-80 overflow-y-auto rounded-md border border-border-subtle">
                {single.plan.moves.map((move) => (
                  <li key={move.media_object_id} className="flex flex-col border-b border-border-subtle px-2 py-1 last:border-0">
                    <span className="break-all font-mono text-ink-muted">{move.source_rel_path}</span>
                    <span className="break-all font-mono">→ {move.dest_rel_path}</span>
                    <Companions move={move} />
                  </li>
                ))}
              </ul>
            </>
          )}
          {bulk && (
            <div className="flex flex-col gap-1">
              <p>
                {bulk.works} can go: {bulk.files} {bulk.files === 1 ? "file" : "files"}, {formatBytes(bulk.bytes)}.
              </p>
              {bulk.refused.length > 0 && (
                <ul className="max-h-48 overflow-y-auto text-ink-muted">
                  {bulk.refused.map((refusal) => (
                    <li key={refusal.work_id}>
                      {refusal.title || `#${refusal.work_id}`} stays: {refusal.reason}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}
        </div>
      )}
    </Dialog>
  );
}
