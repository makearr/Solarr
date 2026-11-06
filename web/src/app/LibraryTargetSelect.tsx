import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { targetTitle } from "@/lib/targetDisplay";

type LibraryQuality = components["schemas"]["LibraryQuality"];

/** The library's target quality, changed in place from the list of libraries: the one setting a library needs.
 *  The rest of its quality (profiles, cutoff, delay) is kept as it is. */
export function LibraryTargetSelect({ categoryId, name }: { categoryId: number; name: string }) {
  const queryClient = useQueryClient();
  const quality = useQuery({
    queryKey: ["libraryQuality", categoryId],
    queryFn: () => ok(api.GET("/categories/{id}/quality", { params: { path: { id: categoryId } } })),
  });
  const save = useMutation({
    mutationFn: (choice: string) => {
      const current = quality.data as LibraryQuality;
      return ok(
        api.PUT("/categories/{id}/quality", {
          params: { path: { id: categoryId } },
          body: {
            profile_ids: current.profiles.map((profile) => profile.id),
            cutoff_profile_id: current.cutoff_profile_id ?? undefined,
            target_id: choice === "" || choice === "auto" ? undefined : Number(choice),
            target_auto: choice === "auto",
          },
        }),
      );
    },
    onSuccess: (data: LibraryQuality) => queryClient.setQueryData(["libraryQuality", categoryId], data),
  });
  if (!quality.data) return null;
  const targets = quality.data.available_targets ?? [];
  if (targets.length === 0 && !quality.data.target) return null;
  const value = quality.data.target_auto ? "auto" : (quality.data.target?.id ?? "");
  return (
    <label className="flex items-center gap-1 text-xs text-ink-muted">
      Target
      <select
        aria-label={`Target quality of ${name}`}
        value={String(value)}
        disabled={save.isPending}
        onChange={(event) => save.mutate(event.target.value)}
        className="max-w-56 rounded-md border border-border-subtle bg-canvas px-1 py-1 text-xs text-ink"
      >
        <option value="">None: the profiles decide</option>
        <option value="auto" disabled={quality.data.profiles.length === 0}>
          Automatic (from the first profile)
        </option>
        {targets.map((target) => (
          <option key={target.id} value={target.id}>
            {targetTitle(target)}
          </option>
        ))}
      </select>
    </label>
  );
}
