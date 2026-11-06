import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { targetTitle } from "@/lib/targetDisplay";

type LibraryQuality = components["schemas"]["LibraryQuality"];
type QualityWrite = components["schemas"]["LibraryQualityWrite"];

/** The library's quality as it stands, which the PUT replaces whole: a change to one part resends the others. */
function useLibraryQuality(categoryId: number) {
  const queryClient = useQueryClient();
  const quality = useQuery({
    queryKey: ["libraryQuality", categoryId],
    queryFn: () => ok(api.GET("/categories/{id}/quality", { params: { path: { id: categoryId } } })),
  });
  const save = useMutation({
    mutationFn: (change: Partial<QualityWrite>) => {
      const current = quality.data as LibraryQuality;
      const keep: QualityWrite = {
        profile_ids: current.profiles.map((profile) => profile.id),
        cutoff_profile_id: current.cutoff_profile_id ?? undefined,
        target_id: current.target_auto ? undefined : current.target?.id,
        target_auto: current.target_auto ?? false,
      };
      return ok(api.PUT("/categories/{id}/quality", { params: { path: { id: categoryId } }, body: { ...keep, ...change } }));
    },
    onSuccess: (data: LibraryQuality) => queryClient.setQueryData(["libraryQuality", categoryId], data),
  });
  return { quality: quality.data, save };
}

const selectStyle = "max-w-56 rounded-md border border-border-subtle bg-canvas px-1 py-1 text-xs text-ink";

/** The library's target quality, changed in place from the list of libraries: the one setting a library needs.
 *  The rest of its quality (profiles, cutoff, delay) is kept as it is. */
export function LibraryTargetSelect({ categoryId, name }: { categoryId: number; name: string }) {
  const { quality, save } = useLibraryQuality(categoryId);
  if (!quality) return null;
  const targets = quality.available_targets ?? [];
  if (targets.length === 0 && !quality.target) return null;
  const value = quality.target_auto ? "auto" : (quality.target?.id ?? "");
  return (
    <label className="flex items-center gap-1 text-xs text-ink-muted">
      Target
      <select
        aria-label={`Target quality of ${name}`}
        value={String(value)}
        disabled={save.isPending}
        onChange={(event) => {
          const choice = event.target.value;
          save.mutate({ target_id: choice === "" || choice === "auto" ? undefined : Number(choice), target_auto: choice === "auto" });
        }}
        className={selectStyle}
      >
        <option value="">None: the profiles decide</option>
        <option value="auto" disabled={quality.profiles.length === 0}>
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

/** The library's language, beside its target: both are chosen without a quality profile. */
export function LibraryLanguageSelect({ categoryId, name }: { categoryId: number; name: string }) {
  const { quality, save } = useLibraryQuality(categoryId);
  if (!quality) return null;
  const languages = quality.available_languages ?? [];
  if (languages.length === 0 && quality.language_profile_id == null) return null;
  return (
    <label className="flex items-center gap-1 text-xs text-ink-muted">
      Language
      <select
        aria-label={`Language of ${name}`}
        value={String(quality.language_profile_id ?? "")}
        disabled={save.isPending}
        onChange={(event) => save.mutate({ language_profile_id: Number(event.target.value) || 0 })}
        className={selectStyle}
      >
        <option value="">No preference</option>
        {languages.map((language) => (
          <option key={language.id} value={language.id}>
            {language.name}
          </option>
        ))}
      </select>
    </label>
  );
}
