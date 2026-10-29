import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { keepsEnabled as staysEnabled, sameOptions, toggleChoice, VERDICT_ORDER } from "@/lib/importLists";
import { useQualityProfiles } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { Dialog } from "@/ui/Dialog";
import { inputStyle, labelStyle } from "@/ui/form";

type ImportList = components["schemas"]["ImportList"];
type ImportListWrite = components["schemas"]["ImportListWrite"];
type ImportListTypes = components["schemas"]["ImportListTypes"];
type ImportListField = components["schemas"]["ImportListField"];
type PreviewEntry = components["schemas"]["ImportListPreviewEntry"];
type Verdict = PreviewEntry["verdict"];

/** The write body that keeps a list as it is. */
export function writeOf(list: ImportList): ImportListWrite {
  return {
    name: list.name, type: list.type, options: list.options, cadence_hours: list.cadence_hours, monitor: list.monitor,
    monitor_option: list.monitor_option, quality_profile_id: list.quality_profile_id, exclusions: list.exclusions,
    enabled: list.enabled,
  };
}

/** Adding or editing a list: its type's own settings as the server declares them, then how what it adds is kept. */
export function ImportListForm({
  libraryId,
  mediaType,
  list,
  types,
  onClose,
  onSaved,
}: {
  libraryId: number;
  mediaType: string | undefined;
  list: ImportList | undefined;
  types: ImportListTypes;
  onClose: () => void;
  onSaved: (saved: ImportList, preview: boolean) => void;
}) {
  const [name, setName] = useState(list?.name ?? "");
  const [type, setType] = useState(list?.type ?? types.types[0]?.type ?? "");
  const [options, setOptions] = useState<Record<string, string>>(list?.options ?? {});
  const [cadence, setCadence] = useState(list?.cadence_hours ?? 24);
  const [monitor, setMonitor] = useState(list?.monitor ?? true);
  const [monitorOption, setMonitorOption] = useState(list?.monitor_option ?? "");
  const [profileId, setProfileId] = useState(list?.quality_profile_id ?? 0);
  const [exclusions, setExclusions] = useState(list?.exclusions ?? "");
  const profiles = useQualityProfiles();
  const kind = types.types.find((candidate) => candidate.type === type);

  // New settings are previewed again before the list adds anything (ADR-38), so such an edit is saved disabled.
  const settingsChanged = list !== undefined && (list.type !== type || !sameOptions(list.options, options));
  const keepsEnabled = staysEnabled(list, type, options);

  const save = useMutation({
    mutationFn: async () => {
      const kept: Record<string, string> = {};
      for (const field of kind?.fields ?? []) {
        const value = options[field.key]?.trim();
        if (value) kept[field.key] = value;
      }
      const body: ImportListWrite = {
        name: name.trim(), type, options: kept, cadence_hours: cadence, monitor, monitor_option: monitorOption,
        quality_profile_id: profileId || undefined, exclusions, enabled: keepsEnabled,
      };
      if (list === undefined) {
        return ok(api.POST("/libraries/{id}/import-lists", { params: { path: { id: libraryId } }, body }));
      }
      return ok(api.PATCH("/import-lists/{id}", { params: { path: { id: list.id } }, body }));
    },
    onSuccess: (saved) => onSaved(saved, !keepsEnabled),
  });

  const missing = !name.trim() || !kind || kind.fields.some((field) => field.required && !options[field.key]?.trim());

  return (
    <Dialog
      title={list ? t("importLists.form.editTitle", { name: list.name }) : t("importLists.form.addTitle")}
      description={kind?.help}
      width="wide"
      onClose={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>{t("importLists.form.cancel")}</Button>
          <Button variant="primary" disabled={missing} busy={save.isPending} onClick={() => save.mutate()}>
            {keepsEnabled ? t("importLists.form.save") : t("importLists.form.saveAndPreview")}
          </Button>
        </>
      }
    >
      <form
        className="grid grid-cols-1 gap-3 md:grid-cols-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (!missing) save.mutate();
        }}
      >
        <label className={labelStyle}>
          {t("importLists.form.name")}
          <input className={inputStyle} value={name} autoComplete="off" onChange={(event) => setName(event.target.value)} />
        </label>
        <label className={labelStyle}>
          {t("importLists.form.type")}
          <select className={inputStyle} value={type} onChange={(event) => setType(event.target.value)}>
            {types.types.map((candidate) => (
              <option key={candidate.type} value={candidate.type}>{candidate.label}</option>
            ))}
          </select>
        </label>

        {kind?.fields.map((field) => (
          <FieldInput
            key={`${type}:${field.key}`}
            field={field}
            value={options[field.key] ?? ""}
            onChange={(value) => setOptions((previous) => ({ ...previous, [field.key]: value }))}
          />
        ))}

        <label className={labelStyle}>
          {t("importLists.form.cadence")}
          <input
            className={inputStyle}
            type="number"
            min={1}
            value={cadence}
            onChange={(event) => setCadence(Math.max(1, Number(event.target.value)))}
          />
        </label>
        <label className={labelStyle}>
          {t("importLists.form.profile")}
          <select className={inputStyle} value={profileId} onChange={(event) => setProfileId(Number(event.target.value))}>
            <option value={0}>{t("importLists.form.profileDefault")}</option>
            {(profiles.data ?? [])
              .filter((profile) => profile.media_type === mediaType)
              .map((profile) => (
                <option key={profile.id} value={profile.id}>{profile.name}</option>
              ))}
          </select>
        </label>
        <label className="flex items-center gap-2 self-end text-xs text-ink-muted">
          <input type="checkbox" checked={monitor} onChange={(event) => setMonitor(event.target.checked)} />
          {t("importLists.form.monitor")}
        </label>
        {types.monitor_options.length > 0 && (
          <label className={labelStyle}>
            {t("importLists.form.monitorOption")}
            <select
              className={inputStyle}
              value={monitorOption}
              disabled={!monitor}
              onChange={(event) => setMonitorOption(event.target.value)}
            >
              <option value="">{t("importLists.form.monitorDefault")}</option>
              {types.monitor_options.map((choice) => (
                <option key={choice.key} value={choice.key}>{choice.label}</option>
              ))}
            </select>
          </label>
        )}
        <label className={`${labelStyle} md:col-span-2`}>
          {t("importLists.form.exclusions")}
          <textarea className={inputStyle} rows={2} value={exclusions} onChange={(event) => setExclusions(event.target.value)} />
          <span>{t("importLists.form.exclusionsHelp")}</span>
        </label>
        {settingsChanged && list?.enabled && (
          <p className="text-xs text-warn md:col-span-2">{t("importLists.form.reviewAgain")}</p>
        )}
        {save.isError && (
          <p className="text-xs text-danger md:col-span-2">
            {(save.error as { detail?: string }).detail ?? t("importLists.form.failed")}
          </p>
        )}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

function FieldInput({ field, value, onChange }: { field: ImportListField; value: string; onChange: (value: string) => void }) {
  const label = field.required ? `${field.label} *` : field.label;
  if (field.kind === "choice") {
    return (
      <label className={labelStyle}>
        {label}
        <select className={inputStyle} value={value || field.default} onChange={(event) => onChange(event.target.value)}>
          {!field.default && <option value="">{t("importLists.form.pick")}</option>}
          {field.choices.map((choice) => (
            <option key={choice.key} value={choice.key}>{choice.label}</option>
          ))}
        </select>
        {field.help && <span>{field.help}</span>}
      </label>
    );
  }
  if (field.kind === "choices") {
    const picked = (value || field.default).split(",").filter(Boolean);
    const toggle = (key: string) => onChange(toggleChoice(field.choices.map((choice) => choice.key), picked, key));
    return (
      <fieldset className={`${labelStyle} md:col-span-2`}>
        <legend className="mb-1">{label}</legend>
        <span className="flex flex-wrap gap-3">
          {field.choices.map((choice) => (
            <label key={choice.key} className="flex items-center gap-1.5 text-sm text-ink">
              <input type="checkbox" checked={picked.includes(choice.key)} onChange={() => toggle(choice.key)} />
              {choice.label}
            </label>
          ))}
        </span>
        {field.help && <span>{field.help}</span>}
      </fieldset>
    );
  }
  return (
    <label className={labelStyle}>
      {label}
      <input
        className={inputStyle}
        value={value}
        inputMode={field.kind === "year" ? "numeric" : undefined}
        placeholder={field.default}
        autoComplete="off"
        onChange={(event) => onChange(event.target.value)}
      />
      {field.help && <span>{field.help}</span>}
    </label>
  );
}

const VERDICT_STYLE: Record<Verdict, string> = {
  add: "border-accent text-accent",
  unchecked: "border-border-subtle text-ink-muted",
  held: "border-border-subtle text-ink-muted",
  handled: "border-border-subtle text-ink-muted",
  excluded: "border-border-subtle text-ink-muted",
  filtered: "border-border-subtle text-ink-muted",
  foreign: "border-border-subtle text-ink-muted",
  unresolved: "border-warn text-warn",
};

const ROW_HEIGHT = 36;

/** What enabling a list would add, read now and adding nothing (ADR-38); enabling is offered from here. */
export function ImportListPreview({ list, onClose }: { list: ImportList; onClose: () => void }) {
  const queryClient = useQueryClient();
  const preview = useQuery({
    queryKey: ["importListPreview", list.id],
    gcTime: 0,
    retry: false,
    queryFn: () => ok(api.POST("/import-lists/{id}/preview", { params: { path: { id: list.id } } })),
  });
  const enable = useMutation({
    mutationFn: async () => {
      await ok(api.PATCH("/import-lists/{id}", { params: { path: { id: list.id } }, body: { ...writeOf(list), enabled: true } }));
      await ok(api.POST("/import-lists/{id}/run", { params: { path: { id: list.id } } }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["importLists"] }),
  });

  const entries = useMemo(
    () => [...(preview.data?.entries ?? [])].sort((a, b) => VERDICT_ORDER.indexOf(a.verdict) - VERDICT_ORDER.indexOf(b.verdict)),
    [preview.data],
  );
  const counts = VERDICT_ORDER.map((verdict) => [verdict, entries.filter((entry) => entry.verdict === verdict).length] as const)
    .filter(([, count]) => count > 0);

  return (
    <Dialog
      title={t("importLists.preview.title", { name: list.name })}
      description={t("importLists.preview.description")}
      width="wide"
      onClose={onClose}
      closeLabel={t("importLists.preview.close")}
      actions={
        <>
          {enable.isSuccess && (
            <span className="mr-auto self-center text-xs text-ok">
              {t("importLists.preview.enabled", { hours: list.cadence_hours })}
            </span>
          )}
          {enable.isError && <span className="mr-auto self-center text-xs text-danger">{t("importLists.refused")}</span>}
          <Button variant="ghost" onClick={onClose}>{t("importLists.preview.close")}</Button>
          {!list.enabled && !enable.isSuccess && (
            <Button variant="primary" disabled={!preview.isSuccess} busy={enable.isPending} onClick={() => enable.mutate()}>
              {t("importLists.preview.enable")}
            </Button>
          )}
        </>
      }
    >
      {preview.isLoading && <p className="text-sm text-ink-muted">{t("importLists.preview.reading")}</p>}
      {preview.isError && (
        <p className="text-sm text-danger">{(preview.error as { detail?: string }).detail ?? t("importLists.preview.failed")}</p>
      )}
      {preview.isSuccess && entries.length === 0 && <p className="text-sm text-ink-muted">{t("importLists.preview.empty")}</p>}
      {entries.length > 0 && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap gap-1.5">
            {counts.map(([verdict, count]) => (
              <span key={verdict} className={`rounded-full border px-2 py-px text-xs ${VERDICT_STYLE[verdict]}`}>
                {t(`importLists.verdict.${verdict}`)}: {count}
              </span>
            ))}
          </div>
          <PreviewRows entries={entries} />
        </div>
      )}
    </Dialog>
  );
}

function PreviewRows({ entries }: { entries: PreviewEntry[] }) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 10,
  });
  return (
    <div ref={scrollRef} className="h-[min(50vh,28rem)] overflow-y-auto rounded-md border border-border-subtle">
      <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
        {virtualizer.getVirtualItems().map((row) => {
          const entry = entries[row.index];
          if (!entry) return null;
          return (
            <div
              key={`${entry.provider}:${entry.external_id}`}
              className="absolute inset-x-0 flex items-center gap-2 border-b border-border-subtle px-3 text-sm"
              style={{ height: row.size, transform: `translateY(${row.start}px)` }}
            >
              <span className="min-w-0 flex-1 truncate">
                {entry.title}
                {entry.year ? <span className="text-ink-muted"> ({entry.year})</span> : null}
              </span>
              <span className="hidden font-mono text-[0.6875rem] text-ink-muted md:inline">
                {entry.match_provider ? `${entry.match_provider}:${entry.match_external_id}` : `${entry.provider}:${entry.external_id}`}
              </span>
              <span className={`shrink-0 rounded-full border px-1.5 py-px text-[0.6875rem] ${VERDICT_STYLE[entry.verdict]}`}>
                {t(`importLists.verdict.${entry.verdict}`)}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
