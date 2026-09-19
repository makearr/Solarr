import { useCallback } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

const KEY = ["me", "preferences"] as const;

/** One of the caller's own stored interface preferences -- sort order,
 *  chosen columns, whether a poster carries its availability bar. Kept on
 *  the server per user, not in localStorage, because the same person on a
 *  second machine asked for the same list they left (2026-09-19). Falls back
 *  to `fallback` while loading and whenever nothing is stored. */
export function usePreference<T>(key: string, fallback: T): [T, (value: T) => void] {
  const queryClient = useQueryClient();
  const stored = useQuery({
    queryKey: KEY,
    queryFn: async () => {
      const { data, error } = await api.GET("/me/preferences");
      if (error) throw error;
      return data;
    },
  });

  const save = useMutation({
    mutationFn: async (value: T) => {
      const { error } = await api.PUT("/me/preferences/{key}", {
        params: { path: { key } },
        body: { value: JSON.stringify(value) },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: KEY }),
  });

  const raw = stored.data?.[key];
  let value = fallback;
  if (raw !== undefined) {
    try {
      value = JSON.parse(raw) as T;
    } catch {
      value = fallback;
    }
  }

  // Optimistic: the list reorders on click rather than after a round trip.
  const set = useCallback(
    (next: T) => {
      queryClient.setQueryData(KEY, (current: Record<string, string> | undefined) => ({
        ...(current ?? {}),
        [key]: JSON.stringify(next),
      }));
      save.mutate(next);
    },
    [key, queryClient, save],
  );

  return [value, set];
}
