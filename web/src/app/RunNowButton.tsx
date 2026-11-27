import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { problemText } from "@/lib/useNotifications";

/** Fires registered tasks at once through the one scheduler endpoint; a task already running is skipped, not queued twice. */
export function RunNowButton({ keys, label = t("scheduler.runNow") }: { keys: readonly string[]; label?: string }) {
  const queryClient = useQueryClient();
  const run = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async () => {
      const results = await Promise.allSettled(keys.map((key) => ok(api.POST("/schedules/{key}/run", { params: { path: { key } } }))));
      const failed = results.find((result): result is PromiseRejectedResult => result.status === "rejected");
      if (failed && results.every((result) => result.status === "rejected")) throw failed.reason;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["schedules"] }),
  });
  return (
    <span className="flex shrink-0 flex-col items-end gap-1">
      <button
        type="button"
        disabled={run.isPending}
        onClick={() => run.mutate()}
        className="rounded-md border border-border-subtle px-3 py-1 text-xs hover:bg-surface disabled:opacity-50"
      >
        {label}
      </button>
      {run.isSuccess && <span className="text-xs text-ok">{t("scheduler.runStarted")}</span>}
      {run.isError && <span className="max-w-48 text-xs text-danger">{problemText(run.error)}</span>}
    </span>
  );
}
