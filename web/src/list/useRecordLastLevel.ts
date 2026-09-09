import { useEffect } from "react";

import { api } from "@/api/client";

/** Persists ADR-54's last-visited level whenever a category/level pair is
 *  actually viewed, so the sidebar tree costs one click on return, not
 *  three. Best-effort: a failed write here is not worth surfacing to the
 *  viewer over a per-browser convenience. */
export function useRecordLastLevel(categoryId: string | undefined, level: string) {
  useEffect(() => {
    if (!categoryId || !level) return;
    void api.PUT("/libraries/{id}/last-level", {
      params: { path: { id: Number(categoryId) } },
      body: { level },
    });
  }, [categoryId, level]);
}
