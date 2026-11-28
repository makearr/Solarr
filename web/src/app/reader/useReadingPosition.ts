import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef } from "react";

import { api } from "@/api/client";

/** The caller's saved place in a file (undefined while it loads, null when there is none) and a saver that writes the
 *  latest place a moment after the last change, so turning pages quickly makes one request. */
export function useReadingPosition(mediaObjectId: number) {
  const saved = useQuery({
    queryKey: ["reading-position", mediaObjectId],
    queryFn: async () => {
      const { data, response } = await api.GET("/files/{mediaObjectId}/reading-position", { params: { path: { mediaObjectId } } });
      if (response.status === 404) return null;
      return data ?? null;
    },
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const latest = useRef<{ position: string; progress: number } | null>(null);
  const flush = useCallback(() => {
    clearTimeout(timer.current);
    const place = latest.current;
    latest.current = null;
    if (place) void api.PUT("/files/{mediaObjectId}/reading-position", { params: { path: { mediaObjectId } }, body: place });
  }, [mediaObjectId]);
  const save = useCallback(
    (position: string, progress: number) => {
      latest.current = { position, progress: Math.min(Math.max(progress, 0), 1) };
      clearTimeout(timer.current);
      timer.current = setTimeout(flush, 800);
    },
    [flush],
  );
  // Leaving the reader keeps the last place at once.
  useEffect(() => flush, [flush]);
  return { saved: saved.isPending ? undefined : saved.data, save };
}
