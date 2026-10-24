import { useCallback, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

const KEY = ["me", "preferences"] as const;

/** One of the caller's own stored interface preferences -- sort order,
 *  chosen columns, whether a poster carries its availability bar. Kept on
 *  the server per user, not in localStorage, because the same person on a
 *  second machine asked for the same list they left (2026-09-19). Falls back
 *  to `fallback` while loading and whenever nothing is stored. */
export function usePreference<T>(key: string, fallback: T): [T, (value: T) => void, boolean] {
  const queryClient = useQueryClient();
  const stored = useQuery({
    queryKey: KEY,
    queryFn: () => ok(api.GET("/me/preferences")),
  });
  // Keep the optimistic value until the cache's raw value advances, avoiding
  // a one-render flicker between the click and the optimistic cache update.
  const [justSet, setJustSet] = useState<{ value: T; over: string | undefined; token: number }>();
  const optimisticID = useRef(0);

  const save = useMutation({
    // One key's writes go out in order, so an earlier one cannot land last and win.
    scope: { id: `preference.${key}` },
    mutationFn: async ({ value }: { value: T; token: number }) => {
      await ok(api.PUT("/me/preferences/{key}", {
        params: { path: { key } },
        body: { value: JSON.stringify(value) },
      }));
    },
    // Success keeps the optimistic value: a refetch after one save could land after the next and undo it (2026-10-23).
    onError: (_error, variables) => {
      setJustSet((current) => (current?.token === variables.token ? undefined : current));
      void queryClient.invalidateQueries({ queryKey: KEY });
    },
  });

  const raw = stored.data?.[key];
  // The value just set, until the stored one moves (the query's own update arrives a tick later), so a checkbox does not
  // flick back for a frame.
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
      const token = ++optimisticID.current;
      setJustSet({ value: next, over: raw, token });
      void queryClient.cancelQueries({ queryKey: KEY });
      queryClient.setQueryData(KEY, (current: Record<string, string> | undefined) => ({
        ...(current ?? {}),
        [key]: JSON.stringify(next),
      }));
      save.mutate({ value: next, token });
    },
    [key, queryClient, save, raw],
  );

  return [justSet && justSet.over === raw ? justSet.value : value, set, stored.isSuccess];
}
