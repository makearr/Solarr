import { useCallback, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

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
    queryFn: () => ok(api.GET("/me/preferences")),
  });

  const save = useMutation({
    mutationFn: async (value: T) => {
      await ok(api.PUT("/me/preferences/{key}", {
        params: { path: { key } },
        body: { value: JSON.stringify(value) },
      }));
    },
    // Success keeps the optimistic value: a refetch after one save could land after the next and undo it (2026-10-23).
    onError: () => void queryClient.invalidateQueries({ queryKey: KEY }),
  });

  const raw = stored.data?.[key];
  // The value just set, until the stored one moves (the query's own update arrives a tick later), so a checkbox does not
  // flick back for a frame.
  const [justSet, setJustSet] = useState<{ value: T; over: string | undefined }>();
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
      setJustSet({ value: next, over: raw });
      void queryClient.cancelQueries({ queryKey: KEY });
      queryClient.setQueryData(KEY, (current: Record<string, string> | undefined) => ({
        ...(current ?? {}),
        [key]: JSON.stringify(next),
      }));
      save.mutate(next);
    },
    [key, queryClient, save, raw],
  );

  return [justSet && justSet.over === raw ? justSet.value : value, set];
}
