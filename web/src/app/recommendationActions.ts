import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { showToast } from "@/lib/toast";

type Recommendation = components["schemas"]["Recommendation"];

/** Dismissing a recommendation, with an Undo toast; every place that lists recommendations shares it. */
export function useRecommendationDismissal() {
  const queryClient = useQueryClient();
  const refresh = () => {
    for (const key of ["discover-dashboard", "discover-recommendations", "discover-dismissals"]) {
      void queryClient.invalidateQueries({ queryKey: [key] });
    }
  };
  const restore = useMutation({
    meta: { success: t("discover.restored") },
    mutationFn: (item: Pick<Recommendation, "provider" | "external_id" | "kind">) =>
      ok(api.DELETE("/discover/dismissals/{provider}/{external_id}", {
        params: { path: { provider: item.provider, external_id: item.external_id }, query: { kind: item.kind } },
      })),
    onSuccess: refresh,
  });
  const dismiss = useMutation({
    mutationFn: (item: Recommendation) => ok(api.POST("/discover/recommendations/{id}/dismiss", { params: { path: { id: item.id } } })),
    onSuccess: (_, item) => {
      refresh();
      showToast({ message: t("discover.hidden", { title: item.title }), action: { label: t("toast.undo"), run: () => restore.mutate(item) } });
    },
  });
  return { dismiss: dismiss.mutate, restore: restore.mutate, restoring: restore.isPending, restoringItem: restore.variables };
}
