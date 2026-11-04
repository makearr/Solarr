import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";

/** Says where a request of the person's ranks against the automatic searches of the library it is added to. */
export function AddPriority({ categoryId }: { categoryId: number }) {
  const priority = useQuery({
    queryKey: ["request-priority", categoryId],
    queryFn: () => ok(api.GET("/priority/request", { params: { query: { category_id: categoryId } } })),
  });
  if (!priority.data) return null;
  const { request, best_automatic: automatic } = priority.data;
  return (
    <p className="flex items-center gap-2 text-xs text-ink-muted">
      <span className="w-24" />
      {t(request > automatic ? "add.priorityFirst" : "add.priorityShared", { request, automatic })}
    </p>
  );
}
