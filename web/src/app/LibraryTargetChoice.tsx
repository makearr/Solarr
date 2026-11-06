import { useState } from "react";
import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { familyOf, preferredTarget, targetTitle } from "@/lib/targetDisplay";
import { inputStyle, labelStyle } from "@/ui/form";

/** The target quality a library about to be created starts with: the preferred one of its family unless another is picked.
 *  The server already gives a new library that default; `apply` only changes it when the choice differs. */
export function useTargetChoice(mediaType: string | undefined) {
  const targets = useQuery({ queryKey: ["qualityTargets"], queryFn: () => ok(api.GET("/qualitytargets")) });
  // undefined: untouched, so the default stands; 0: none.
  const [picked, setPicked] = useState<number>();
  const family = mediaType ? familyOf(mediaType) : "";
  const choices = (targets.data ?? []).filter((target) => target.media_type === family);
  const fallback = mediaType ? preferredTarget(targets.data ?? [], mediaType)?.id : undefined;
  const value = picked ?? fallback ?? 0;
  return {
    choices,
    value,
    setPicked,
    /** Saves a differing choice on the library just made. */
    apply: async (categoryId: number) => {
      if (picked === undefined || picked === fallback) return;
      await ok(
        api.PUT("/categories/{id}/quality", {
          params: { path: { id: categoryId } },
          body: { profile_ids: [], target_id: picked || undefined },
        }),
      );
    },
  };
}

export type TargetChoice = ReturnType<typeof useTargetChoice>;

/** The target quality as a field of the library form. */
export function TargetChoiceField({ choice }: { choice: TargetChoice }) {
  if (choice.choices.length === 0) return null;
  return (
    <label className={labelStyle}>
      Target quality
      <select className={inputStyle} value={choice.value} onChange={(event) => choice.setPicked(Number(event.target.value))}>
        <option value={0}>None</option>
        {choice.choices.map((target) => (
          <option key={target.id} value={target.id}>
            {targetTitle(target)}
          </option>
        ))}
      </select>
    </label>
  );
}
