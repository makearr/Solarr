import { usePreference } from "@/lib/usePreference";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibraries } from "@/lib/resources";
import { sizeOff } from "@/lib/targetCloseness";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { inputStyle } from "@/ui/form";

import { PlayButton } from "./player/PlayButton";
import { formatBytes } from "./Queue";
import { TargetChip } from "./work/TargetChip";
import { fileTagChips } from "./work/workFormat";

type Group = components["schemas"]["SimilarVideoReport"]["groups"][number];
type Video = Group["videos"][number];
type Level = "exact" | "high" | "medium" | "low";
type Action = "link" | "recycle";

/** One library's videos, or every library's (administrators only). */
export type SimilarScope = { categoryId: number } | "everywhere";

const LEVELS: readonly { id: Level; label: string; help: string }[] = [
  { id: "exact", label: "Exact", help: "identical picture hashes" },
  { id: "high", label: "High", help: "up to 4 of 64 bits apart" },
  { id: "medium", label: "Medium", help: "up to 8 bits apart" },
  { id: "low", label: "Low", help: "up to 10 bits apart; finds more, and wrong ones too" },
];

const extensionOf = (path: string) => (path.match(/\.[^./\\]+$/)?.[0] ?? "").toLowerCase();
const episodeTokenOf = (path: string) => path.match(/S\d+E\d+/i)?.[0]?.toUpperCase() ?? "";

/** File names that say two different episodes outweigh a picture match; the group is flagged, not hidden. */
const namesOtherEpisodes = (group: Group) =>
  new Set(group.videos.map((video) => episodeTokenOf(video.path)).filter((token) => token !== "")).size > 1;

const formatDuration = (ms: number) => {
  const seconds = Math.round(ms / 1000);
  return `${Math.floor(seconds / 3600)}:${String(Math.floor((seconds % 3600) / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
};

/** Merging links the dropped file to the kept one: the same library, and a file of the same kind to link to. */
const mergeable = (video: Video, keep: Video) => video.category_id === keep.category_id && extensionOf(video.path) === extensionOf(keep.path);

/** How far a file is from the library's target, either way; a file with no target to judge by is furthest. */
const distanceFromTarget = (video: Video) => (video.file.target_size_pct == null ? Number.POSITIVE_INFINITY : sizeOff(video.file.target_size_pct));

/** The file to keep unasked: the one closest to its library's target, the larger on a tie (and the largest when no target judges). */
function closestToTarget(videos: readonly Video[]): Video {
  return videos.reduce((best, video) => {
    const gap = distanceFromTarget(video) - distanceFromTarget(best);
    return gap < 0 || (gap === 0 && video.size_bytes > best.size_bytes) ? video : best;
  });
}

/** How many of the 64 bits of the two videos' picture hashes differ. */
export function bitsApart(first: string, second: string): number {
  let rest = BigInt(`0x${first}`) ^ BigInt(`0x${second}`);
  let count = 0;
  while (rest > 0n) {
    count += Number(rest & 1n);
    rest >>= 1n;
  }
  return count;
}

const nameOf = (path: string) => path.slice(path.lastIndexOf("/") + 1);

type Pending = { drops: Video[]; action: Action };

function GroupCard({ group, scope, libraryName }: { group: Group; scope: SimilarScope; libraryName: (id: number) => string }) {
  const queryClient = useQueryClient();
  const [kept, setKept] = useState<ReadonlySet<number>>(() => new Set([closestToTarget(group.videos).media_object_id]));
  const [pending, setPending] = useState<Pending | null>(null);
  const keptVideos = group.videos.filter((video) => kept.has(video.media_object_id));
  // The file merges link to, and the one the bits apart are counted from: the kept file nearest the target.
  const primary = keptVideos.length > 0 ? closestToTarget(keptVideos) : undefined;
  const unkept = group.videos.filter((video) => !kept.has(video.media_object_id));
  const actionOf = (video: Video, action: Action): Action => (action === "link" && primary && mergeable(video, primary) ? "link" : "recycle");
  const apply = useMutation({
    mutationFn: ({ drops, action }: Pending) => {
      const body = {
        keep_media_object_id: (primary as Video).media_object_id,
        drop: drops.map((video) => ({ media_object_id: video.media_object_id, action: actionOf(video, action) })),
      };
      return scope === "everywhere"
        ? ok(api.POST("/duplicates/similar-videos/resolve", { body }))
        : ok(api.POST("/categories/{id}/duplicates/resolve", { params: { path: { id: scope.categoryId } }, body }));
    },
    onSuccess: () => {
      setPending(null);
      void queryClient.invalidateQueries({ queryKey: ["similar-videos"] });
    },
  });
  const done = apply.data;
  const toggle = (video: Video) =>
    setKept((current) => {
      const next = new Set(current);
      if (!next.delete(video.media_object_id)) next.add(video.media_object_id);
      return next;
    });
  const freed = unkept.reduce((total, video) => total + video.size_bytes, 0);
  const linking = pending?.drops.filter((video) => actionOf(video, pending.action) === "link") ?? [];
  const deleting = pending?.drops.filter((video) => actionOf(video, pending.action) === "recycle") ?? [];
  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
      <div className="flex flex-wrap items-baseline gap-x-3">
        <span className="font-medium">{group.videos.length} files, at most {group.max_distance} of 64 bits apart</span>
        <span className="ml-auto text-ink-muted">frees up to {formatBytes(freed)}</span>
      </div>
      {group.videos.map((video) => {
        const isKept = kept.has(video.media_object_id);
        const bits = primary && primary.media_object_id !== video.media_object_id ? bitsApart(primary.mean_hash, video.mean_hash) : undefined;
        const canMerge = primary !== undefined && !isKept && mergeable(video, primary);
        return (
          <div key={video.media_object_id} className={`flex flex-col gap-1 rounded-md border px-2.5 py-1.5 ${isKept ? "border-accent bg-accent/5" : "border-border-subtle"}`}>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <label className="flex items-center gap-1.5">
                <input type="checkbox" checked={isKept} onChange={() => toggle(video)} aria-label={`Keep ${video.work_title || nameOf(video.path)}`} />
                <span className="font-medium">{isKept ? "Keep" : "Not kept"}</span>
              </label>
              {video.work_id > 0 ? (
                <Link
                  to="/library/$categoryId/works/$workId"
                  params={{ categoryId: String(video.category_id), workId: String(video.work_id) }}
                  className="text-sm font-medium hover:underline"
                  title="Open it in its library"
                >
                  {video.work_title || nameOf(video.path)}
                </Link>
              ) : (
                <span className="text-sm font-medium">{nameOf(video.path)}</span>
              )}
              <PlayButton extension={video.file.extension} item={{ mediaObjectId: video.media_object_id, title: video.work_title || nameOf(video.path), durationMs: video.duration_ms || undefined, file: video.file }} />
              {scope === "everywhere" && <Chip>{libraryName(video.category_id)}</Chip>}
              {bits !== undefined && <Chip title="Bits of the 64-bit picture hash that differ from the kept file">{bits} {bits === 1 ? "bit" : "bits"} apart</Chip>}
              {video.duration_ms > 0 && <span className="text-ink-muted">{formatDuration(video.duration_ms)}</span>}
            </div>
            <div className="flex flex-wrap items-center gap-1">
              {fileTagChips(video.file).map((chip) => (
                <Chip key={chip.text} tone={chip.tone}>{chip.text}</Chip>
              ))}
              <TargetChip file={video.file} />
            </div>
            <span className="break-all font-mono text-ink-muted">{video.path}</span>
            {video.path_count > 1 && <span className="text-warn">The same file is linked at {video.path_count} paths; dropping it affects all of them.</span>}
            {!isKept && !done && (
              <div className="flex flex-wrap gap-2">
                <Button
                  disabled={!canMerge}
                  title={canMerge ? undefined : primary ? "Only a file in the same library and of the same kind can be merged; delete it instead." : "Keep at least one file first."}
                  onClick={() => setPending({ drops: [video], action: "link" })}
                >
                  Merge into the kept file
                </Button>
                <Button disabled={primary === undefined} onClick={() => setPending({ drops: [video], action: "recycle" })}>
                  Delete
                </Button>
              </div>
            )}
          </div>
        );
      })}
      {namesOtherEpisodes(group) && <p className="text-warn">These file names say different episodes. Check them before keeping one.</p>}
      {primary === undefined && <p className="text-warn">Tick at least one file to keep.</p>}
      {done ? (
        <p className="text-ok">
          Done: {done.linked} merged, {done.recycled} deleted.
          {done.problems.map((problem) => (
            <span key={problem} className="block text-warn">{problem}</span>
          ))}
        </p>
      ) : pending && primary ? (
        <div className="flex flex-col gap-2 rounded-md border border-warn p-2">
          {linking.length > 0 && (
            <p>
              <span className="font-medium">Merge:</span> {nameOf(primary.path)} is kept. {linking.length === 1 ? "This file's" : "These files'"} location{linking.length === 1 ? "" : "s"} become a hardlink
              (a reflink where the filesystem offers it) to the kept file, so every path keeps working and the bytes are stored once. The old copy waits in the
              recycle bin for 7 days.
              {linking.map((video) => (
                <span key={video.media_object_id} className="block break-all font-mono text-ink-muted">{video.path}</span>
              ))}
            </p>
          )}
          {deleting.length > 0 && (
            <p>
              <span className="font-medium text-warn">Delete:</span> moved to the recycle bin (restorable for 7 days); the {deleting.length === 1 ? "work it is on" : "works they are on"} will miss{" "}
              {deleting.length === 1 ? "it" : "them"}.
              {deleting.map((video) => (
                <span key={video.media_object_id} className="block break-all font-mono text-ink-muted">{video.path}</span>
              ))}
            </p>
          )}
          <div className="flex gap-2">
            <Button onClick={() => apply.mutate(pending)} disabled={apply.isPending}>{apply.isPending ? "Working…" : "Confirm"}</Button>
            <Button onClick={() => setPending(null)}>Cancel</Button>
          </div>
        </div>
      ) : (
        primary &&
        unkept.length > 1 && (
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => setPending({ drops: unkept, action: "link" })}>Merge all the others into the kept file…</Button>
            <Button onClick={() => setPending({ drops: unkept, action: "recycle" })}>Delete all the others…</Button>
          </div>
        )
      )}
      {apply.isError && <p className="text-warn">That could not be done; nothing was changed.</p>}
    </li>
  );
}

/** Videos that look like one cut in several encodes, by perceptual hash, with the similarity chosen as in Stash. The
 *  user picks which file stays; the others are merged into it or deleted. */
export function SimilarVideos({ scope }: { scope: SimilarScope }) {
  const search = useSearch({ strict: false }) as { level?: string; library?: string };
  const navigate = useNavigate();
  // Exact until you choose otherwise; the choice is kept for next time.
  const [preferred, setPreferred] = usePreference<Level>("similar-videos.level", "exact");
  const known = (value: string | undefined): value is Level => LEVELS.some((entry) => entry.id === value);
  const level: Level = known(search.level) ? search.level : known(preferred) ? preferred : "exact";
  const libraries = useLibraries();
  const report = useQuery({
    queryKey: ["similar-videos", scope === "everywhere" ? "everywhere" : scope.categoryId, level],
    refetchOnMount: "always",
    queryFn: () =>
      scope === "everywhere"
        ? ok(api.GET("/duplicates/similar-videos", { params: { query: { level } } }))
        : ok(api.GET("/categories/{id}/similar-videos", { params: { path: { id: scope.categoryId }, query: { level } } })),
  });
  const libraryName = (id: number) => libraries.data?.find((library) => library.id === id)?.name ?? `Library ${id}`;
  const data = report.data;
  // Libraries that hold a file of some group, with how many groups touch each; "All" is every library's groups.
  const groupsByLibrary = new Map<number, number>();
  for (const group of data?.groups ?? []) {
    for (const id of new Set(group.videos.map((video) => video.category_id))) groupsByLibrary.set(id, (groupsByLibrary.get(id) ?? 0) + 1);
  }
  const chosenLibrary = scope === "everywhere" && search.library && groupsByLibrary.has(Number(search.library)) ? Number(search.library) : undefined;
  const shown = (data?.groups ?? []).filter((group) => chosenLibrary === undefined || group.videos.some((video) => video.category_id === chosenLibrary));
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-medium">Similar videos</h2>
        {scope === "everywhere" && (
          <label className="ml-auto flex items-center gap-1.5 text-xs text-ink-muted">
            Library
            <select
              aria-label="Library"
              className={inputStyle}
              value={chosenLibrary ?? ""}
              onChange={(event) => void navigate({ to: ".", search: (previous: object) => ({ ...previous, library: event.target.value || undefined }), replace: true })}
            >
              <option value="">All</option>
              {[...groupsByLibrary].map(([id, count]) => (
                <option key={id} value={id}>{libraryName(id)} ({count})</option>
              ))}
            </select>
          </label>
        )}
        <label className={`${scope === "everywhere" ? "" : "ml-auto "}flex items-center gap-1.5 text-xs text-ink-muted`}>
          Similarity
          <select
            aria-label="Similarity"
            className={inputStyle}
            value={level}
            onChange={(event) => {
              setPreferred(event.target.value as Level);
              void navigate({ to: ".", search: (previous: object) => ({ ...previous, level: event.target.value }), replace: true });
            }}
          >
            {LEVELS.map((entry) => (
              <option key={entry.id} value={entry.id}>{entry.label} — {entry.help}</option>
            ))}
          </select>
        </label>
      </div>
      <p className="max-w-xl text-xs text-ink-muted">
        Files that look like the same video in another encode, size or container{scope === "everywhere" ? ", in any library" : ""}.
        Only files the hash pass has reached are compared, so more can appear over time.
        {data?.limited ? " Only the largest groups are shown." : ""}
      </p>
      {data && !data.hashing_enabled && (
        <p className="text-xs text-warn">
          Video hashing is switched off, so files added since are not compared.
          {scope === "everywhere" ? "" : " An administrator can turn it on under Storage › Duplicates."}
        </p>
      )}
      {report.isLoading && <p className="text-xs text-ink-muted">Looking…</p>}
      {report.isError && <p className="text-xs text-danger">The report could not be made.</p>}
      {data && shown.length === 0 && <p className="text-xs text-ink-muted">No similar videos found at this level.</p>}
      {data && shown.length > 0 && (
        <ul className="flex flex-col gap-3">
          {shown.map((group) => (
            <GroupCard key={group.videos.map((video) => video.media_object_id).join("-")} group={group} scope={scope} libraryName={libraryName} />
          ))}
        </ul>
      )}
    </section>
  );
}
