import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type Tier = components["schemas"]["Me"]["tier"];

const TIER_RANK: Record<Tier, number> = { normal: 0, advanced: 1, expert: 2 };

/** The caller's own tier (ADR-26): how much of the settings surface they are
 *  shown. Shares App.tsx's own ["me"] query cache rather than a Context --
 *  react-query already dedupes by key, so every consumer reads the same
 *  fetch with no provider tree to wire up. Fails open to "expert" (show
 *  everything) while loading or on error, never to "normal" (hide
 *  everything): a field silently disappearing with no explanation is worse
 *  than one shown a beat before the real tier is known. */
export function useTier(): Tier {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me");
      if (error) throw error;
      return data;
    },
  });
  return me.data?.tier ?? "expert";
}

/** Whether something declared at `required` should show to a caller
 *  currently at `tier` -- ADR-26's own ordering (normal < advanced <
 *  expert): a higher tier sees everything a lower one does. */
export function isTierVisible(tier: Tier, required: Tier): boolean {
  return TIER_RANK[tier] >= TIER_RANK[required];
}

/** Changes the caller's own tier (self-service, PATCH /me/tier -- any
 *  authenticated user, not just an administrator) and refreshes every
 *  cached query that might now show or hide something, the same blast
 *  radius useSetAuthRequired's own invalidation already uses. */
export function useSetMyTier() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (tier: Tier) => {
      const { data, error } = await api.PATCH("/me/tier", { body: { tier } });
      if (error) throw error;
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries(),
  });
}
