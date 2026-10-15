import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";

type Plan = components["schemas"]["NumberingPlan"];

/** The lists a work can be numbered by, when it has more than one; nothing otherwise. */
export function useNumberingChoices(workId: number, enabled: boolean) {
  return useQuery({
    queryKey: ["work-numbering", workId],
    enabled,
    queryFn: async () => {
      const { data, error, response } = await api.GET("/works/{id}/numbering", { params: { path: { id: workId } } });
      if (error) throw error;
      return response.status === 204 ? null : (data ?? null);
    },
  });
}

/** Sonarr's and Medusa's DVD order, as SickGear switches a show's list: preview every episode that moves, then apply. */
export function NumberingDialog({ workId, title, onClose }: { workId: number; title: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const numbering = useNumberingChoices(workId, true);
  const [basis, setBasis] = useState<string>();
  const chosen = basis ?? numbering.data?.current ?? "";
  const change = useMutation({
    mutationFn: async (preview: boolean) => {
      const { data, error } = await api.POST("/works/{id}/numbering", { params: { path: { id: workId } }, body: { basis: chosen, preview } });
      if (error) throw error;
      return data;
    },
    onSuccess: (_, preview) => {
      if (preview) return;
      void queryClient.invalidateQueries({ queryKey: ["work", workId] });
      void queryClient.invalidateQueries({ queryKey: ["work-numbering", workId] });
    },
  });
  const preview: Plan | undefined = change.variables === true ? change.data : undefined;
  const applied = change.variables === false && change.isSuccess;
  return (
    <Dialog title={t("numbering.title")} description={title} width="wide" onClose={onClose}>
      {numbering.data ? (
        <div className="flex flex-col gap-3 text-sm">
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            {t("numbering.by")}
            <select
              className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink"
              value={chosen}
              onChange={(event) => {
                setBasis(event.target.value);
                change.reset();
              }}
            >
              {numbering.data.choices.map((choice) => (
                <option key={choice.key} value={choice.key}>
                  {choice.label}
                </option>
              ))}
            </select>
          </label>
          {preview && <PlanView plan={preview} />}
          {applied && <p className="text-ok">{t("numbering.applied")}</p>}
          {change.isError && <p className="text-danger">{(change.error as { detail?: string }).detail ?? t("numbering.failed")}</p>}
          <div className="flex gap-2">
            <Button busy={change.isPending && change.variables === true} onClick={() => change.mutate(true)} disabled={chosen === numbering.data.current}>
              {t("numbering.preview")}
            </Button>
            {preview && (
              <Button variant="primary" busy={change.isPending && change.variables === false} onClick={() => change.mutate(false)}>
                {t("numbering.apply")}
              </Button>
            )}
          </div>
        </div>
      ) : (
        <p className="text-sm text-ink-muted">{numbering.isLoading ? t("work.loading") : t("numbering.none")}</p>
      )}
    </Dialog>
  );
}

function PlanView({ plan }: { plan: Plan }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-ink-muted">{t("numbering.summary", { moves: plan.moves.length, unchanged: plan.unchanged, new: plan.new })}</p>
      {plan.moves.length > 0 && (
        <ul className="max-h-72 overflow-y-auto rounded-md border border-border-subtle text-xs">
          {plan.moves.map((move, index) => (
            <li key={index} className="flex gap-3 border-b border-border-subtle px-2 py-1 last:border-0">
              <span className="w-16 shrink-0 font-mono">{move.from}</span>
              <span aria-hidden>→</span>
              <span className="w-16 shrink-0 font-mono">{move.to}</span>
              <span className="truncate">{move.title}</span>
              {move.has_file && <span className="ml-auto shrink-0 text-ok">{t("numbering.hasFile")}</span>}
            </li>
          ))}
        </ul>
      )}
      {plan.set_aside.length > 0 && (
        <p className="text-warn">{t("numbering.setAside", { count: plan.set_aside.length, list: plan.set_aside.map((move) => move.from).join(", ") })}</p>
      )}
      <p className="text-xs text-ink-muted">{t("numbering.renameAfter")}</p>
    </div>
  );
}
