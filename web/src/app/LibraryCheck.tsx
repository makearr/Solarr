import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams } from "@tanstack/react-router";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { EmptyState } from "@/ui/EmptyState";

import { LibraryMoveDialog, type MoveSubject } from "./work/LibraryMoveDialog";

type Misplaced = components["schemas"]["MisplacedWork"];

/** What in this library belongs in another by the libraries' rules, to move or copy there, and the kinds of library
 *  some of it asks for that do not exist yet (plan 10-libraries L5). */
export function LibraryCheck() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const id = Number(categoryId);
  const administrator = useIsAdministrator();
  const queryClient = useQueryClient();
  const mediaTypes = useMediaTypes();
  const check = useQuery({ queryKey: ["library-check", id], queryFn: () => ok(api.GET("/categories/{id}/check", { params: { path: { id } } })) });
  const [moving, setMoving] = useState<{ subject: MoveSubject; mediaType: string; from: number; to: number }>();

  if (check.isLoading) return <p className="p-6 text-sm text-ink-muted">Checking…</p>;
  if (check.error !== null || !check.data) return <p className="p-6 text-sm text-danger">The library could not be checked.</p>;
  const groups = new Map<string, Misplaced[]>();
  for (const work of check.data.misplaced) {
    const key = `${work.from_category_id}:${work.suggestion.category_id}`;
    groups.set(key, [...(groups.get(key) ?? []), work]);
  }
  const typeName = (mediaType: string) => mediaTypes.find((info) => info.key === mediaType)?.work.many.toLowerCase() ?? mediaType;

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 p-4">
      {check.data.missing_kinds.map((kind) => (
        <div key={`${kind.media_type}:${kind.kind}`} className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle bg-surface p-3 text-sm">
          <span>
            No {kind.label} library for {typeName(kind.media_type)} yet: {kind.works} here would go there.
          </span>
          {administrator && (
            <Link to="/settings/libraries" className="ml-auto text-accent hover:underline">
              Add a {kind.label} library
            </Link>
          )}
        </div>
      ))}
      {check.data.misplaced.length === 0 ? (
        <EmptyState title="Everything here belongs here." hint="Each library's rules (its settings, Suggested for) say what belongs in it." />
      ) : (
        [...groups.values()].map((works) => {
          const first = works[0]!;
          return (
            <section key={`${first.from_category_id}:${first.suggestion.category_id}`} className="flex flex-col gap-2">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-sm font-semibold">
                  {works.length} {works.length === 1 ? "belongs" : "belong"} in {first.suggestion.name}
                </h2>
                {administrator && (
                  <Button
                    onClick={() => setMoving({
                      subject: { selection: { ids: works.map((work) => work.work_id) }, count: works.length },
                      mediaType: first.media_type, from: first.from_category_id, to: first.suggestion.category_id,
                    })}
                  >
                    Move or copy all…
                  </Button>
                )}
              </div>
              <ul className="flex flex-col rounded-md border border-border-subtle">
                {works.map((work) => (
                  <li key={work.work_id} className="flex flex-wrap items-center gap-2 border-b border-border-subtle px-3 py-1.5 text-sm last:border-0">
                    <Link
                      to="/library/$categoryId/works/$workId"
                      params={{ categoryId: String(work.from_category_id), workId: String(work.work_id) }}
                      className="font-medium hover:underline"
                    >
                      {work.title}
                    </Link>
                    <span className="text-xs text-ink-muted">{work.suggestion.reason}</span>
                    {administrator && (
                      <button
                        type="button"
                        onClick={() => setMoving({ subject: { workId: work.work_id, title: work.title }, mediaType: work.media_type, from: work.from_category_id, to: work.suggestion.category_id })}
                        className="ml-auto text-xs text-ink-muted hover:text-ink"
                      >
                        Move…
                      </button>
                    )}
                  </li>
                ))}
              </ul>
            </section>
          );
        })
      )}
      {moving && (
        <LibraryMoveDialog
          subject={moving.subject}
          mediaType={moving.mediaType}
          fromLibraryId={moving.from}
          initialTarget={moving.to}
          onClose={() => setMoving(undefined)}
          onDone={() => {
            setMoving(undefined);
            void queryClient.invalidateQueries({ queryKey: ["library-check", id] });
          }}
        />
      )}
    </div>
  );
}
