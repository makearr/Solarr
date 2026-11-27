import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { isTierVisible, useTier } from "@/lib/useTier";

import { SimilarVideos } from "./SimilarVideos";

/** Whether videos are hashed in the background; off, hashes already stored still serve the finder. */
function HashingSwitch() {
  const queryClient = useQueryClient();
  const saved = useQuery({ queryKey: ["video-hashing"], queryFn: () => ok(api.GET("/settings/video-hashing")) });
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (body: { enabled: boolean; parallel_files?: number; frame_bits?: number }) => ok(api.PUT("/settings/video-hashing", { body })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["video-hashing"] });
      void queryClient.invalidateQueries({ queryKey: ["similar-videos"] });
    },
  });
  const tier = useTier();
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  if (!saved.data) return null;
  const current = saved.data;
  const previewsOn = general.data?.video_previews === true;
  return (
    <div className="flex flex-col gap-2">
    <label
      className={`flex items-start gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs ${previewsOn ? "opacity-60" : ""}`}
      title={previewsOn ? t("settings.hashFree") : undefined}
    >
      <input
        type="checkbox"
        className="mt-0.5 size-3.5"
        checked={current.enabled || previewsOn}
        disabled={save.isPending || previewsOn}
        onChange={(event) => save.mutate({ enabled: event.target.checked })}
      />
      <span className="flex flex-col gap-0.5">
        <span className="font-medium">{t("videoDuplicates.hashVideosInThe")}</span>
        <span className="text-ink-muted">
          Reads nine frames of every video with ffmpeg so re-encodes can be found. Switch it off to save the load; hashes already
          stored keep working.
        </span>
        {save.isError && <span className="text-danger">{t("importLists.refused")}</span>}
      </span>
    </label>
    {isTierVisible(tier, "advanced") && (
      <div className="flex flex-wrap gap-4 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
        <label className="flex flex-col gap-1">
          <span className="font-medium">{t("videoDuplicates.parallelFiles")}</span>
          <input
            type="number"
            min={1}
            max={8}
            className="w-20 rounded-md border border-border-subtle bg-surface px-2 py-1"
            defaultValue={current.parallel_files}
            key={`parallel-${current.parallel_files}`}
            disabled={save.isPending}
            onBlur={(event) => {
              const value = Number(event.target.value);
              if (value !== current.parallel_files) save.mutate({ enabled: current.enabled, parallel_files: value });
            }}
          />
          <span className="max-w-xs text-ink-muted">{t("videoDuplicates.parallelFilesHelp")}</span>
        </label>
        <label className="flex flex-col gap-1">
          <span className="font-medium">{t("videoDuplicates.frameBits")}</span>
          <input
            type="number"
            min={2}
            max={20}
            className="w-20 rounded-md border border-border-subtle bg-surface px-2 py-1"
            defaultValue={current.frame_bits}
            key={`bits-${current.frame_bits}`}
            disabled={save.isPending}
            onBlur={(event) => {
              const value = Number(event.target.value);
              if (value !== current.frame_bits) save.mutate({ enabled: current.enabled, frame_bits: value });
            }}
          />
          <span className="max-w-xs text-ink-muted">{t("videoDuplicates.frameBitsHelp")}</span>
        </label>
      </div>
    )}
    </div>
  );
}

/** What an import does with a download whose picture the library already holds. */
function ImportSwitches() {
  const queryClient = useQueryClient();
  const saved = useQuery({ queryKey: ["import-duplicates"], queryFn: () => ok(api.GET("/settings/import-duplicates")) });
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (body: { check: boolean; blocklist: boolean }) => ok(api.PUT("/settings/import-duplicates", { body })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["import-duplicates"] }),
  });
  if (!saved.data) return null;
  const current = saved.data;
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs">
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-0.5 size-3.5"
          checked={current.check}
          disabled={save.isPending}
          onChange={(event) => save.mutate({ ...current, check: event.target.checked })}
        />
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{t("videoDuplicates.checkImportsForDuplicates")}</span>
          <span className="text-ink-muted">
            A downloaded episode is compared by its picture with the series&apos; files. A copy counts as the episode it shows, whatever the
            release called it, and replaces that file only when it is better.
          </span>
        </span>
      </label>
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-0.5 size-3.5"
          checked={current.blocklist}
          disabled={save.isPending || !current.check}
          onChange={(event) => save.mutate({ ...current, blocklist: event.target.checked })}
        />
        <span className="flex flex-col gap-0.5">
          <span className="font-medium">{t("videoDuplicates.blocklistReleasesThatDuplicate")}</span>
          <span className="text-ink-muted">
            Off, the same release can be grabbed again by a later search. On, it is kept out for 30 days.
          </span>
          {save.isError && <span className="text-danger">{t("importLists.refused")}</span>}
        </span>
      </label>
    </div>
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
      <ImportSwitches />
      <SimilarVideos scope="everywhere" />
    </div>
  );
}
