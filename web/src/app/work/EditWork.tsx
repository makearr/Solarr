import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import type { MediaTypeInfo } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";

type Work = components["schemas"]["WorkDetail"];

interface Draft {
  monitored: boolean;
  profileId: number | null;
  tags: string;
  fields: Record<string, string>;
  option: string;
  specials: boolean;
}

const field = "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm";

/** Radarr's and Sonarr's Edit: monitored, a monitor option where the type has them, the work's own quality profile,
 *  its tags and the type's editable fields (a series' type). */
export function EditWork({ work, info, onClose }: { work: Work; info: MediaTypeInfo | undefined; onClose: () => void }) {
  const queryClient = useQueryClient();
  const edit = useQuery({
    queryKey: ["work-edit", work.id],
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}/edit", { params: { path: { id: work.id } } });
      if (error) throw error;
      return data;
    },
  });
  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const editable = (info?.facets ?? []).filter((facet) => facet.editable);
  const [draft, setDraft] = useState<Draft>();
  const form: Draft | undefined =
    draft ??
    (edit.data && {
      monitored: edit.data.monitored,
      profileId: edit.data.profile_id,
      tags: edit.data.tags.join(", "),
      fields: Object.fromEntries(editable.map((facet) => [facet.key, edit.data.fields?.[facet.key] ?? ""])),
      option: "",
      specials: false,
    });
  const change = (next: Partial<Draft>) => form && setDraft({ ...form, ...next });

  const save = useMutation({
    mutationFn: async () => {
      if (!form) return;
      const tags = form.tags.split(",").map((tag) => tag.trim()).filter(Boolean);
      const { error } = await api.PUT("/works/{id}/edit", {
        params: { path: { id: work.id } },
        body: { monitored: form.monitored, profile_id: form.profileId, tags, fields: form.fields },
      });
      if (error) throw error;
      if (form.option) {
        const { error: optionError } = await api.POST("/works/{id}/monitor", {
          params: { path: { id: work.id } },
          body: { option: form.option, specials: form.specials },
        });
        if (optionError) throw optionError;
      }
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["work", work.id] });
      void queryClient.invalidateQueries({ queryKey: ["work-edit", work.id] });
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
      onClose();
    },
  });

  return (
    <Dialog
      title={`${t("action.edit")}: ${work.title}`}
      onClose={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("action.cancel")}
          </Button>
          <Button variant="primary" busy={save.isPending} disabled={!form} onClick={() => save.mutate()}>
            Save
          </Button>
        </>
      }
    >
      {!form ? (
        <p className="text-sm text-ink-muted">{t("work.loading")}</p>
      ) : (
        <div className="flex flex-col gap-3 text-sm">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={form.monitored} onChange={(event) => change({ monitored: event.target.checked })} />
            {t("action.monitor")}
          </label>
          {(info?.monitor_options.length ?? 0) > 0 && (
            <label className="flex flex-col gap-1">
              <span className="text-xs text-ink-muted">Monitor which {info?.part.many.toLowerCase() || "parts"}</span>
              <span className="flex flex-wrap items-center gap-2">
                <select value={form.option} onChange={(event) => change({ option: event.target.value })} className={field}>
                  <option value="">Keep as they are</option>
                  {info?.monitor_options.map((option) => (
                    <option key={option.key} value={option.key}>
                      {option.label}
                    </option>
                  ))}
                </select>
                {form.option && (
                  <label className="flex items-center gap-1 text-xs text-ink-muted">
                    <input type="checkbox" checked={form.specials} onChange={(event) => change({ specials: event.target.checked })} /> also the specials
                  </label>
                )}
              </span>
            </label>
          )}
          <label className="flex flex-col gap-1">
            <span className="text-xs text-ink-muted">Quality profile</span>
            <select
              value={form.profileId ?? 0}
              onChange={(event) => change({ profileId: Number(event.target.value) || null })}
              className={field}
            >
              <option value={0}>The library&apos;s default</option>
              {(profiles.data ?? [])
                .filter((profile) => profile.media_type === work.media_type)
                .map((profile) => (
                  <option key={profile.id} value={profile.id}>
                    {profile.name}
                  </option>
                ))}
            </select>
          </label>
          {editable.map((facet) => (
            <label key={facet.key} className="flex flex-col gap-1">
              <span className="text-xs text-ink-muted">{facet.label}</span>
              <select value={form.fields[facet.key] ?? ""} onChange={(event) => change({ fields: { ...form.fields, [facet.key]: event.target.value } })} className={field}>
                <option value="">Automatic{work.facets?.[facet.key]?.[0] ? ` (now ${work.facets[facet.key]?.[0]})` : ""}</option>
                {(facet.choices ?? []).map((choice) => (
                  <option key={choice.key} value={choice.key}>
                    {choice.label}
                  </option>
                ))}
              </select>
            </label>
          ))}
          <label className="flex flex-col gap-1">
            <span className="text-xs text-ink-muted">Tags, separated by commas</span>
            <input value={form.tags} onChange={(event) => change({ tags: event.target.value })} className={field} />
          </label>
          {save.isError && <p className="text-xs text-danger">{(save.error as { detail?: string }).detail ?? "That could not be saved."}</p>}
        </div>
      )}
    </Dialog>
  );
}
