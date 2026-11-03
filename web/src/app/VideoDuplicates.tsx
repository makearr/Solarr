import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

import { SimilarVideos } from "./SimilarVideos";

/** Whether videos are hashed in the background; off, hashes already stored still serve the finder. */
function HashingSwitch() {
  const queryClient = useQueryClient();
  const saved = useQuery({ queryKey: ["video-hashing"], queryFn: () => ok(api.GET("/settings/video-hashing")) });
  const save = useMutation({
    mutationFn: (enabled: boolean) => ok(api.PUT("/settings/video-hashing", { body: { enabled } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["video-hashing"] });
      void queryClient.invalidateQueries({ queryKey: ["similar-videos"] });
    },
  });
  if (!saved.data) return null;
  return (
    <label className="flex items-start gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
      <input
        type="checkbox"
        className="mt-0.5 size-3.5"
        checked={saved.data.enabled}
        disabled={save.isPending}
        onChange={(event) => save.mutate(event.target.checked)}
      />
      <span className="flex flex-col gap-0.5">
        <span className="font-medium">Hash videos in the background</span>
        <span className="text-ink-muted">
          Reads seven frames of every video with ffmpeg so re-encodes can be found. Switch it off to save the load; hashes already
          stored keep working.
        </span>
        {save.isError && <span className="text-danger">That change was refused.</span>}
      </span>
    </label>
  );
}

/** Videos held more than once across every library; administrators only. One library's are under its own Files. */
export function VideoDuplicates() {
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Duplicates</h1>
        <p className="text-sm text-ink-muted">
          The same video stored more than once anywhere in the libraries. You choose the file to keep; each other file is merged into it
          (linked, when it is in the same library and on the same filesystem) or moved to the recycle bin.
        </p>
      </header>
      <HashingSwitch />
      <SimilarVideos scope="everywhere" />
    </div>
  );
}
