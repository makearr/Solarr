import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";

const inputStyle = "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink";

/** Puts a work in one of the library's collections made by hand, or in a new one. */
export function AddToCollection({ categoryId, workId, title, onClose }: { categoryId: string; workId: number; title: string; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [chosen, setChosen] = useState("");
  const [name, setName] = useState("");
  const list = useQuery({
    queryKey: ["franchises", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/franchises", { params: { query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const mine = (list.data?.franchises ?? []).filter((franchise) => franchise.kind === "custom");
  const add = useMutation({
    mutationFn: async () => {
      let id = Number(chosen);
      if (!chosen) {
        const { data, error } = await api.POST("/franchises", { body: { category_id: Number(categoryId), name } });
        if (error) throw error;
        id = data.id;
      }
      const { error } = await api.PUT("/franchises/{id}/members/{workId}", { params: { path: { id, workId }, query: { category_id: Number(categoryId) } } });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["franchises", categoryId] });
      onClose();
    },
  });
  const ready = chosen !== "" || name.trim() !== "";
  return (
    <Dialog
      title={t("collection.add.title", { title })}
      description={t("collection.add.help")}
      onClose={onClose}
      actions={
        <>
          <Button onClick={onClose}>{t("action.cancel")}</Button>
          <Button variant="primary" busy={add.isPending} disabled={!ready} onClick={() => add.mutate()}>
            {t("collection.add.confirm")}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3">
        {mine.length > 0 && (
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            {t("collection.add.existing")}
            <select className={inputStyle} value={chosen} onChange={(event) => setChosen(event.target.value)}>
              <option value="">{t("collection.add.new")}</option>
              {mine.map((franchise) => (
                <option key={franchise.id} value={franchise.id}>
                  {franchise.name}
                </option>
              ))}
            </select>
          </label>
        )}
        {chosen === "" && (
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            {t("collection.add.name")}
            <input className={inputStyle} value={name} onChange={(event) => setName(event.target.value)} placeholder={t("collection.add.placeholder")} />
          </label>
        )}
        {add.isError && <p className="text-xs text-danger">{t("franchise.failed")}</p>}
      </div>
    </Dialog>
  );
}
