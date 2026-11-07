import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Chip } from "@/ui/Chip";

/** What an expert sees beside a work's file: every path and how it was placed, whether renaming would name it
 *  differently, and whether other files may duplicate it. Nothing for lower tiers. */
export function useExpertFiles(workId: number, categoryId: string | undefined) {
  const expert = isTierVisible(useTier(), "expert");
  const details = useQuery({
    queryKey: ["file-details", workId],
    enabled: expert,
    queryFn: () => ok(api.GET("/works/{id}/file-details", { params: { path: { id: workId } } })),
  });
  // The rename preview needs manage access; where it is refused the line just says nothing about renaming.
  const renames = useQuery({
    queryKey: ["file-rename-preview", categoryId, workId],
    enabled: expert && categoryId !== undefined,
    retry: false,
    queryFn: () => ok(api.POST("/categories/{id}/rename", { params: { path: { id: Number(categoryId) } }, body: { preview: true, work_id: workId } })),
  });
  return { expert, details: details.data?.files, renames: renames.isSuccess ? renames.data.roots.flatMap((root) => root.plan.moves) : undefined };
}

export type ExpertFiles = ReturnType<typeof useExpertFiles>;

export function ExpertFileInfo({ mediaObjectId, expert }: { mediaObjectId: number; expert: ExpertFiles }) {
  if (!expert.expert) return null;
  const detail = expert.details?.find((entry) => entry.media_object_id === mediaObjectId);
  const move = expert.renames?.find((entry) => entry.media_object_id === mediaObjectId);
  if (!detail) return null;
  return (
    <span className="mt-1 flex flex-col gap-1 rounded-md border border-border-subtle p-2 text-xs">
      {detail.paths.map((place) => (
        <span key={place.path} className="flex flex-wrap items-center gap-1.5">
          <span className="break-all font-mono text-ink-muted">{place.path}</span>
          <Chip tone={place.link_kind === "copy" ? "neutral" : "accent"} title="How this path was placed in the library">
            {place.link_kind}
          </Chip>
          {place.primary && detail.paths.length > 1 && <Chip>primary</Chip>}
        </span>
      ))}
      {expert.renames !== undefined &&
        (move ? (
          <span className="flex flex-col gap-0.5">
            <span className="text-warn">A rename would name it differently:</span>
            <span className="break-all font-mono text-ink-muted">{move.dest_rel_path}</span>
          </span>
        ) : (
          <span className="text-ink-muted">A rename would leave its name as it is.</span>
        ))}
      <span className="flex flex-wrap items-center gap-1.5">
        {detail.same_bytes > 0 && <Chip tone="warn">{detail.same_bytes} other {detail.same_bytes === 1 ? "file" : "files"} with the same bytes</Chip>}
        {detail.same_picture > 0 && <Chip tone="warn">{detail.same_picture} other {detail.same_picture === 1 ? "video" : "videos"} with the same picture</Chip>}
        {detail.same_bytes === 0 && detail.same_picture === 0 && <span className="text-ink-muted">No duplicate candidates.</span>}
      </span>
    </span>
  );
}
