import { useEffect, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaType } from "@/lib/useMediaTypes";
import type { components } from "@/api/schema";

import { isTierVisible, useTier } from "@/lib/useTier";
import { inputStyle } from "@/ui/form";

import { Message, Toggle } from "./LibraryNaming";
import { SaveBar, SettingRow } from "./SettingsCard";

type FileManagement = components["schemas"]["FileManagement"];
type FileSettingKey = components["schemas"]["MediaTypeInfo"]["file_settings"][number]["key"];

/** Sonarr's Folders, File Management and Permissions sections, for one library (2026-09-19). */
export function LibraryFileManagement() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const queryClient = useQueryClient();
  // Sonarr's basic view: folders and deleted tracks; the recycle bin and the rest are advanced (2026-09-30).
  const advanced = isTierVisible(useTier(), "advanced");
  // The settings whose meaning depends on the type are offered only when it declares them, in its words.
  const declared = useMediaType(useLibrary(categoryId ?? "")?.media_type)?.file_settings ?? [];
  const setting = (key: FileSettingKey) => declared.find((entry) => entry.key === key);
  const options = (entry: { options?: { key: string; label: string }[] }) =>
    (entry.options ?? []).map((option) => [option.key, option.label] as const);

  const saved = useQuery({
    queryKey: ["libraryFileManagement", id],
    enabled: id !== undefined,
    queryFn: () => ok(api.GET("/categories/{id}/file-management", { params: { path: { id: id! } } })),
  });
  const [draft, setDraft] = useState<FileManagement | undefined>();
  useEffect(() => {
    if (saved.data) setDraft(saved.data);
  }, [saved.data]);

  const save = useMutation({
    mutationFn: async (body: FileManagement) => {
      const { data, error } = await api.PUT("/categories/{id}/file-management", {
        params: { path: { id: id! } },
        body,
      });
      if (error) throw new Error(error.detail ?? "refused");
      return data;
    },
    onSuccess: (data) => queryClient.setQueryData(["libraryFileManagement", id], data),
  });

  if (id === undefined) return <Message>No library in the URL.</Message>;
  if (saved.isLoading || !draft) return <Message>Loading…</Message>;
  if (saved.error !== null || !saved.data) return <Message>This library&apos;s file management could not be read.</Message>;

  const dirty = JSON.stringify(draft) !== JSON.stringify(saved.data);
  const set = (patch: Partial<FileManagement>) => setDraft({ ...draft, ...patch });

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-5 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">File management</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          What Solarr does with this library&apos;s folders and files, as in Sonarr&apos;s Media Management.
        </p>
      </header>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Folders</h2>
        <DeclaredToggle entry={setting("create_empty_artist_folders")} checked={draft.create_empty_artist_folders} onChange={(checked) => set({ create_empty_artist_folders: checked })} />
        <DeclaredToggle entry={setting("delete_empty_folders")} checked={draft.delete_empty_folders} onChange={(checked) => set({ delete_empty_folders: checked })} />
      </section>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">File Management</h2>
        <DeclaredToggle entry={setting("unmonitor_deleted")} checked={draft.unmonitor_deleted} onChange={(checked) => set({ unmonitor_deleted: checked })} />
        <DeclaredToggle entry={setting("merge_artist_aliases")} checked={draft.merge_artist_aliases} onChange={(checked) => set({ merge_artist_aliases: checked })} />
        {advanced && (
        <>
        <Choice
          label="Propers and Repacks"
          help="Whether to automatically upgrade to a proper or repack of the same quality."
          value={draft.propers_repacks}
          options={[
            ["prefer_and_upgrade", "Prefer and Upgrade"],
            ["do_not_upgrade", "Do not Upgrade Automatically"],
            ["do_not_prefer", "Do not Prefer"],
          ]}
          onChange={(value) => set({ propers_repacks: value as FileManagement["propers_repacks"] })}
        />
        {setting("rescan_after_refresh") && (
        <Choice
          label={setting("rescan_after_refresh")!.label}
          help={setting("rescan_after_refresh")!.help}
          value={draft.rescan_after_refresh}
          options={[
            ["always", "Always"],
            ["after_manual", "Only After Manual Refresh"],
            ["never", "Never"],
          ]}
          onChange={(value) => set({ rescan_after_refresh: value as FileManagement["rescan_after_refresh"] })}
        />
        )}
        {setting("change_file_date") && (
        <Choice
          label={setting("change_file_date")!.label}
          help={setting("change_file_date")!.help}
          value={draft.change_file_date}
          options={options(setting("change_file_date")!)}
          onChange={(value) => set({ change_file_date: value as FileManagement["change_file_date"] })}
        />
        )}
        </>
        )}
        {advanced && (
        <SettingRow
          label="Recycling Bin Cleanup"
          help="Days, for this library. Deleted files go to each root folder's recycle bin, never straight to deletion, and are removed after this, or sooner once the bin passes its size limit under Storage."
        >
          <input
            type="number"
            min={1}
            max={365}
            value={draft.recycle_retention_days}
            onChange={(event) => set({ recycle_retention_days: Number(event.target.value) || 1 })}
            className={`${inputStyle} w-20`}
          />
        </SettingRow>
        )}
      </section>

      {(setting("import_extra_files") || setting("episode_title_required")) && (
      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Importing</h2>
        {setting("episode_title_required") && (
          <Choice
            label={setting("episode_title_required")!.label}
            help={setting("episode_title_required")!.help}
            value={draft.episode_title_required ?? "always"}
            options={options(setting("episode_title_required")!)}
            onChange={(value) => set({ episode_title_required: value as FileManagement["episode_title_required"] })}
          />
        )}
        <DeclaredToggle entry={setting("import_extra_files")} checked={draft.import_extra_files ?? false} onChange={(checked) => set({ import_extra_files: checked })} />
        {draft.import_extra_files && setting("extra_file_extensions") && (
          <SettingRow label={setting("extra_file_extensions")!.label} help={setting("extra_file_extensions")!.help}>
            <input
              value={(draft.extra_file_extensions ?? []).join(",")}
              onChange={(event) => set({ extra_file_extensions: event.target.value.split(",") })}
              className={`${inputStyle} w-44 font-mono`}
            />
          </SettingRow>
        )}
      </section>
      )}

      {(setting("media_server_files") || setting("monitor_new_seasons") || setting("shared_file_fallback")) && (
        <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
          <h2 className="text-sm font-medium">Media servers</h2>
          <DeclaredToggle entry={setting("media_server_files")} checked={draft.media_server_files ?? false} onChange={(checked) => set({ media_server_files: checked })} />
          <DeclaredToggle entry={setting("monitor_new_seasons")} checked={draft.monitor_new_seasons ?? true} onChange={(checked) => set({ monitor_new_seasons: checked })} />
          {setting("shared_file_fallback") && (
            <Choice
              label={setting("shared_file_fallback")!.label}
              help={setting("shared_file_fallback")!.help}
              value={draft.shared_file_fallback ?? "copy"}
              options={options(setting("shared_file_fallback")!)}
              onChange={(value) => set({ shared_file_fallback: value as FileManagement["shared_file_fallback"] })}
            />
          )}
        </section>
      )}

      {advanced && (
      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Permissions</h2>
        <Toggle
          label="Set Permissions"
          help="Set permissions on files and folders when they are imported or renamed."
          checked={draft.set_permissions}
          onChange={(checked) => set({ set_permissions: checked })}
        />
        {draft.set_permissions && (
          <>
            <SettingRow label="chmod Folder" help="Octal. Files get the same without the execute bits.">
              <input
                value={draft.chmod_folder}
                onChange={(event) => set({ chmod_folder: event.target.value })}
                className={`${inputStyle} w-20 font-mono`}
              />
            </SettingRow>
            <SettingRow label="chown Group" help="Group name or gid. Use the gid for network filesystems.">
              <input
                value={draft.chown_group}
                placeholder="unchanged"
                onChange={(event) => set({ chown_group: event.target.value })}
                className={`${inputStyle} w-32 font-mono`}
              />
            </SettingRow>
          </>
        )}
      </section>
      )}

      <SaveBar dirty={dirty} busy={save.isPending} onSave={() => save.mutate(draft)} onUndo={() => setDraft(saved.data)} error={save.isError ? save.error.message : null} />
    </div>
  );
}

/** A switch for a setting the media type declares; nothing when it does not. */
function DeclaredToggle({
  entry,
  checked,
  onChange,
}: {
  entry: { label: string; help: string } | undefined;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return entry ? <Toggle label={entry.label} help={entry.help} checked={checked} onChange={onChange} /> : null;
}

function Choice({
  label,
  help,
  value,
  options,
  onChange,
}: {
  label: string;
  help: string;
  value: string;
  options: readonly (readonly [string, string])[];
  onChange: (value: string) => void;
}) {
  return (
    <SettingRow label={label} help={help}>
      <select value={value} onChange={(event) => onChange(event.target.value)} className={inputStyle}>
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </select>
    </SettingRow>
  );
}
