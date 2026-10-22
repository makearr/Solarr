import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";

type Rule = components["schemas"]["ReleaseFilter"];

const KINDS: Rule["kind"][] = ["must_contain", "must_not_contain", "prefer", "group_allow", "group_deny"];

/** Sonarr's release profiles and Medusa's per-show require and ignore words, kept on the series itself. */
export function ReleaseFiltersDialog({ workId, title, onClose }: { workId: number; title: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const stored = useQuery({
    queryKey: ["work-release-filters", workId],
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}/release-filters", { params: { path: { id: workId } } });
      if (error) throw error;
      return data ?? [];
    },
  });
  const [edited, setEdited] = useState<Rule[]>();
  const rules = edited ?? stored.data ?? [];
  const change = (index: number, patch: Partial<Rule>) => setEdited(rules.map((rule, at) => (at === index ? { ...rule, ...patch } : rule)));
  const save = useMutation({
    mutationFn: async () => {
      const body = rules.filter((rule) => rule.value.trim() !== "");
      return ok(api.PUT("/works/{id}/release-filters", { params: { path: { id: workId } }, body }));
    },
    onSuccess: (saved) => {
      setEdited(undefined);
      queryClient.setQueryData(["work-release-filters", workId], saved);
    },
  });
  return (
    <Dialog title={t("releaseFilters.title")} description={title} width="wide" onClose={onClose}>
      <div className="flex flex-col gap-3 text-sm">
        <p className="text-xs text-ink-muted">{t("releaseFilters.help")}</p>
        {rules.length === 0 && !stored.isLoading && <p className="text-ink-muted">{t("releaseFilters.none")}</p>}
        {rules.map((rule, index) => (
          <div key={index} className="flex flex-wrap items-center gap-2">
            <select
              aria-label={t("releaseFilters.kind")}
              className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink"
              value={rule.kind}
              onChange={(event) => change(index, { kind: event.target.value as Rule["kind"] })}
            >
              {KINDS.map((kind) => (
                <option key={kind} value={kind}>
                  {t(`releaseFilters.kind.${kind}`)}
                </option>
              ))}
            </select>
            <input
              aria-label={t("releaseFilters.value")}
              className="min-w-40 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink"
              value={rule.value}
              onChange={(event) => change(index, { value: event.target.value })}
            />
            {rule.kind === "prefer" && (
              <input
                aria-label={t("releaseFilters.score")}
                type="number"
                className="w-20 rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink"
                value={rule.score ?? 0}
                onChange={(event) => change(index, { score: Number(event.target.value) })}
              />
            )}
            <input
              aria-label={t("releaseFilters.season")}
              placeholder={t("releaseFilters.seasonHint")}
              className="w-24 rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink"
              value={rule.season_prefix ?? ""}
              onChange={(event) => change(index, { season_prefix: event.target.value })}
            />
            <label className="flex items-center gap-1 text-xs text-ink-muted">
              <input type="checkbox" checked={rule.is_regex ?? false} onChange={(event) => change(index, { is_regex: event.target.checked })} />
              {t("releaseFilters.pattern")}
            </label>
            <button
              type="button"
              aria-label={t("releaseFilters.remove")}
              className="rounded-md p-1 text-ink-muted hover:text-danger"
              onClick={() => setEdited(rules.filter((_, at) => at !== index))}
            >
              <Trash2 aria-hidden className="size-4" />
            </button>
          </div>
        ))}
        {save.isError && <p className="text-danger">{(save.error as { detail?: string }).detail ?? t("releaseFilters.failed")}</p>}
        {save.isSuccess && edited === undefined && <p className="text-ok">{t("releaseFilters.saved")}</p>}
        <div className="flex gap-2">
          <Button icon={<Plus aria-hidden className="size-4" />} onClick={() => setEdited([...rules, { kind: "must_not_contain", value: "", is_regex: false }])}>
            {t("releaseFilters.add")}
          </Button>
          <Button variant="primary" busy={save.isPending} disabled={edited === undefined} onClick={() => save.mutate()}>
            {t("releaseFilters.save")}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}
