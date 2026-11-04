import { useMemo } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

/** What a heart can be on; a song or an episode is a work_part, a studio or a network an organisation. */
export type FavoriteSubject = components["schemas"]["FavoriteSubject"];

const key = (subject: FavoriteSubject) => ["favorites", subject] as const;

/** The ids the caller has hearted, one request per kind shared by every heart on the page. */
export function useFavoriteIds(subject: FavoriteSubject): ReadonlySet<number> {
  const ids = useQuery({
    queryKey: key(subject),
    queryFn: () => ok(api.GET("/favorites", { params: { query: { subject_type: subject } } })),
    staleTime: 60_000,
  });
  return useMemo(() => new Set(ids.data ?? []), [ids.data]);
}

/** Hearts or un-hearts one thing; the heart flips at once and goes back if the server refuses. */
export function useSetFavorite(subject: FavoriteSubject) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, favorite }: { id: number; favorite: boolean }) => {
      const params = { path: { subjectType: subject, id } };
      await ok(favorite ? api.PUT("/favorites/{subjectType}/{id}", { params }) : api.DELETE("/favorites/{subjectType}/{id}", { params }));
    },
    onMutate: async ({ id, favorite }) => {
      await client.cancelQueries({ queryKey: key(subject) });
      const before = client.getQueryData<number[]>(key(subject));
      const rest = (before ?? []).filter((entry) => entry !== id);
      client.setQueryData<number[]>(key(subject), favorite ? [...rest, id] : rest);
      return { before };
    },
    onError: (_error, _vars, context) => client.setQueryData(key(subject), context?.before),
    onSettled: () => client.invalidateQueries({ queryKey: key(subject) }),
  });
}
