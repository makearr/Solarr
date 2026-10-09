import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { t, vocabularyOf } from "@/lib/i18n";
import type { MediaTypeInfo } from "@/lib/useMediaTypes";
import { Pencil } from "lucide-react";

import { useMediaTypes } from "@/lib/useMediaTypes";
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

/** Edit on a tile or row of an index: the work is read when the dialog opens. */
export function EditWorkButton({ id }: { id: number }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        title={t("action.edit")}
        aria-label={t("action.edit")}
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setOpen(true);
        }}
        className="flex size-7 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 text-ink-muted shadow-sm hover:text-ink"
      >
        <Pencil aria-hidden className="size-3.5" />
      </button>
      {open && <EditLoadedWork id={id} onClose={() => setOpen(false)} />}
    </>
  );
}

function EditLoadedWork({ id, onClose }: { id: number; onClose: () => void }) {
  const work = useQuery({
    queryKey: ["work", id],
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
  });
  const info = useMediaTypes().find((entry) => entry.key === work.data?.media_type);
  if (!work.data) return null;
  return <EditWork work={work.data} info={info} onClose={onClose} />;
}

/** A selection as POST /works/bulk/edit takes it. */
export interface BulkSelection {
  ids?: number[];
  category_id?: number;
  excluded_ids?: number[];
  fresh?: boolean;
  where?: string;
}

/** Radarr's and Sonarr's Edit selected: what the dialog sets is applied to every work of the selection after the count
 *  is shown; anything left at "No change" stays as each work has it. */
export function BulkEditWork({
  selection,
  info,
  onClose,
  onDone,
}: {
  selection: BulkSelection;
  info: MediaTypeInfo | undefined;
  onClose: () => void;
  onDone: () => void;
}) {
  const words = vocabularyOf(info);
  const [monitored, setMonitored] = useState<"keep" | "yes" | "no">("keep");
  const [option, setOption] = useState("");
  const [specials, setSpecials] = useState(false);
  const [profile, setProfile] = useState<"keep" | "default" | number>("keep");
  const [addTags, setAddTags] = useState("");
  const [removeTags, setRemoveTags] = useState("");
  const [fields, setFields] = useState<Record<string, string>>({});
  const [count, setCount] = useState<number | null>(null);
  const editable = (info?.facets ?? []).filter((facet) => facet.editable);
  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const names = (raw: string) => raw.split(",").map((name) => name.trim()).filter((name) => name !== "");
  const send = useMutation({
    mutationFn: async (preview: boolean) => {
      const { data, error } = await api.POST("/works/bulk/edit", {
        body: {
          ...selection,
          preview,
          monitored: monitored === "keep" ? undefined : monitored === "yes",
          profile: profile === "keep" ? undefined : { id: profile === "default" ? null : profile },
          add_tags: names(addTags),
          remove_tags: names(removeTags),
          fields: Object.keys(fields).length > 0 ? fields : undefined,
          monitor_option: option || undefined,
          specials,
        },
      });
      if (error) throw error;
      return { count: data.count, preview };
    },
    onSuccess: (result) => {
      if (result.preview) setCount(result.count);
      else onDone();
    },
  });
  const changed = () => setCount(null);
  const nothing =
    monitored === "keep" && option === "" && profile === "keep" && names(addTags).length === 0 && names(removeTags).length === 0 &&
    Object.keys(fields).length === 0;
  return (
    <Dialog
      title={`${t("action.edit")}: the selected ${words.works}`}
      description={`Only what you change is applied; the rest of each ${words.work} stays as it is.`}
      onClose={onClose}
      actions={
        <>
          {count !== null && (
            <span className="mr-auto text-sm">
              Change {count} {count === 1 ? words.work : words.works}?
            </span>
          )}
          <Button variant="ghost" onClick={onClose}>
            {t("action.cancel")}
          </Button>
          {count === null ? (
            <Button variant="primary" disabled={nothing} busy={send.isPending} onClick={() => send.mutate(true)}>
              Review
            </Button>
          ) : (
            <Button variant="primary" busy={send.isPending} busyLabel="Applying…" onClick={() => send.mutate(false)}>
              Apply
            </Button>
          )}
        </>
      }
    >
      <div className="flex flex-col gap-3 text-sm">
        <label className="flex flex-col gap-1">
          <span className="text-xs text-ink-muted">Monitored</span>
          <select value={monitored} onChange={(event) => { setMonitored(event.target.value as "keep" | "yes" | "no"); changed(); }} className={field}>
            <option value="keep">No change</option>
            <option value="yes">{t("action.monitor")}</option>
            <option value="no">{t("action.unmonitor")}</option>
          </select>
        </label>
        {(info?.monitor_options.length ?? 0) > 0 && (
          <label className="flex flex-col gap-1">
            <span className="text-xs text-ink-muted">Monitor which {words.parts}</span>
            <span className="flex flex-wrap items-center gap-2">
              <select value={option} onChange={(event) => { setOption(event.target.value); changed(); }} className={field}>
                <option value="">No change</option>
                {info?.monitor_options.map((entry) => (
                  <option key={entry.key} value={entry.key}>
                    {entry.label}
                  </option>
                ))}
              </select>
              {option && (
                <label className="flex items-center gap-1 text-xs text-ink-muted">
                  <input type="checkbox" checked={specials} onChange={(event) => setSpecials(event.target.checked)} /> also the specials
                </label>
              )}
            </span>
          </label>
        )}
        <label className="flex flex-col gap-1">
          <span className="text-xs text-ink-muted">Quality profile</span>
          <select
            value={String(profile)}
            onChange={(event) => {
              const value = event.target.value;
              setProfile(value === "keep" || value === "default" ? value : Number(value));
              changed();
            }}
            className={field}
          >
            <option value="keep">No change</option>
            <option value="default">The library&apos;s default</option>
            {(profiles.data ?? [])
              .filter((one) => one.media_type === info?.key)
              .map((one) => (
                <option key={one.id} value={one.id}>
                  {one.name}
                </option>
              ))}
          </select>
        </label>
        {editable.map((facet) => (
          <label key={facet.key} className="flex flex-col gap-1">
            <span className="text-xs text-ink-muted">{facet.label}</span>
            <select
              value={fields[facet.key] ?? "keep"}
              onChange={(event) => {
                const rest = Object.fromEntries(Object.entries(fields).filter(([key]) => key !== facet.key));
                setFields(event.target.value === "keep" ? rest : { ...rest, [facet.key]: event.target.value });
                changed();
              }}
              className={field}
            >
              <option value="keep">No change</option>
              <option value="">Automatic</option>
              {(facet.choices ?? []).map((choice) => (
                <option key={choice.key} value={choice.key}>
                  {choice.label}
                </option>
              ))}
            </select>
          </label>
        ))}
        <label className="flex flex-col gap-1">
          <span className="text-xs text-ink-muted">Add tags</span>
          <input value={addTags} onChange={(event) => { setAddTags(event.target.value); changed(); }} placeholder="comma separated" className={field} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs text-ink-muted">Remove tags</span>
          <input value={removeTags} onChange={(event) => { setRemoveTags(event.target.value); changed(); }} placeholder="comma separated" className={field} />
        </label>
        {send.isError && <p className="text-sm text-danger">That could not be applied.</p>}
      </div>
    </Dialog>
  );
}
