import { useNavigate, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useLibraries } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

import { formatBytes } from "./Queue";

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

function GroupCard({ group, scope, libraryName }: { group: Group; scope: SimilarScope; libraryName: (id: number) => string }) {
  const queryClient = useQueryClient();
  const largest = group.videos.reduce((best, video, index) => (video.size_bytes > (group.videos[best]?.size_bytes ?? 0) ? index : best), 0);
  const [keepIndex, setKeepIndex] = useState(largest);
  const [chosen, setChosen] = useState<Record<number, Action>>({});
  const [asking, setAsking] = useState(false);
  const keep = group.videos[keepIndex] ?? (group.videos[0] as Video);
  const others = group.videos.filter((_, index) => index !== keepIndex);
  const actionFor = (video: Video): Action => (chosen[video.media_object_id] === "link" && !mergeable(video, keep) ? "recycle" : (chosen[video.media_object_id] ?? (mergeable(video, keep) ? "link" : "recycle")));
  const apply = useMutation({
    mutationFn: () => {
      const body = {
        keep_media_object_id: keep.media_object_id,
        drop: others.map((video) => ({ media_object_id: video.media_object_id, action: actionFor(video) })),
      };
      return scope === "everywhere"
        ? ok(api.POST("/duplicates/similar-videos/resolve", { body }))
        : ok(api.POST("/categories/{id}/duplicates/resolve", { params: { path: { id: scope.categoryId } }, body }));
    },
    onSuccess: () => {
      setAsking(false);
      void queryClient.invalidateQueries({ queryKey: ["similar-videos"] });
    },
  });
  const done = apply.data;
  const freed = others.reduce((total, video) => total + video.size_bytes, 0);
  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
      <div className="flex flex-wrap items-baseline gap-x-3">
        <span className="font-medium">{group.videos.length} files, at most {group.max_distance} of 64 bits apart</span>
        <span className="ml-auto text-ink-muted">frees up to {formatBytes(freed)}</span>
      </div>
      {group.videos.map((video, index) => (
        <div
          key={video.media_object_id}
          className={`flex flex-col gap-0.5 rounded-md border px-2.5 py-1.5 ${index === keepIndex ? "border-accent bg-accent/5" : "border-border-subtle"}`}
        >
          <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
            <label className="flex items-center gap-1.5">
              <input type="radio" name={`similar-${group.videos[0]?.media_object_id}`} checked={index === keepIndex} onChange={() => setKeepIndex(index)} />
              <span className="font-medium">{index === keepIndex ? "Keep this one" : "Keep"}</span>
            </label>
            <span>{formatBytes(video.size_bytes)}</span>
            <span className="text-ink-muted">{video.width > 0 ? `${video.width}×${video.height}` : "size unknown"}</span>
            <span className="text-ink-muted">{video.duration_ms > 0 ? formatDuration(video.duration_ms) : "length unknown"}</span>
            {scope === "everywhere" && <span className="rounded-full border border-border-subtle px-1.5">{libraryName(video.category_id)}</span>}
            {index !== keepIndex && !done && (
              <label className="ml-auto flex items-center gap-1.5 text-ink-muted">
                Instead
                <select
                  value={actionFor(video)}
                  onChange={(event) => setChosen({ ...chosen, [video.media_object_id]: event.target.value as Action })}
                  className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-ink"
                >
                  <option value="link" disabled={!mergeable(video, keep)}>Merge into the kept file (link)</option>
                  <option value="recycle">Delete (recycle bin)</option>
                </select>
              </label>
            )}
          </div>
          <span className="break-all font-mono text-ink-muted">{video.path}</span>
        </div>
      ))}
      {namesOtherEpisodes(group) && <p className="text-warn">These file names say different episodes. Check them before keeping one.</p>}
      {done ? (
        <p className="text-ok">
          Done: {done.linked} merged, {done.recycled} deleted.
          {done.problems.map((problem) => (
            <span key={problem} className="block text-warn">{problem}</span>
          ))}
        </p>
      ) : asking ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-ink-muted">
            Keep the marked file. {others.filter((video) => actionFor(video) === "link").length} merged into it, {others.filter((video) => actionFor(video) === "recycle").length} moved
            to the recycle bin (restorable for 7 days).
          </span>
          <Button onClick={() => apply.mutate()} disabled={apply.isPending}>{apply.isPending ? "Working…" : "Confirm"}</Button>
          <Button onClick={() => setAsking(false)}>Cancel</Button>
        </div>
      ) : (
        <div>
          <Button onClick={() => setAsking(true)}>Keep the marked file…</Button>
        </div>
      )}
      {apply.isError && <p className="text-warn">That could not be done; nothing was changed.</p>}
    </li>
  );
}

/** Videos that look like one cut in several encodes, by perceptual hash, with the similarity chosen as in Stash. The
 *  user picks which file stays; the others are merged into it or deleted. */
export function SimilarVideos({ scope }: { scope: SimilarScope }) {
  const search = useSearch({ strict: false }) as { level?: string };
  const navigate = useNavigate();
  const level: Level = LEVELS.some((entry) => entry.id === search.level) ? (search.level as Level) : "medium";
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
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
      <div className="flex flex-wrap items-center gap-3">
        <h2 className="text-sm font-medium">Similar videos</h2>
        <label className="ml-auto flex items-center gap-1.5 text-xs text-ink-muted">
          Similarity
          <select
            aria-label="Similarity"
            className={inputStyle}
            value={level}
            onChange={(event) => void navigate({ to: ".", search: (previous: object) => ({ ...previous, level: event.target.value }), replace: true })}
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
      {data && data.groups.length === 0 && <p className="text-xs text-ink-muted">No similar videos found at this level.</p>}
      {data && data.groups.length > 0 && (
        <ul className="flex flex-col gap-3">
          {data.groups.map((group) => (
            <GroupCard key={group.videos.map((video) => video.media_object_id).join("-")} group={group} scope={scope} libraryName={libraryName} />
          ))}
        </ul>
      )}
    </section>
  );
}
