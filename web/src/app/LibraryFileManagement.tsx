import { useEffect, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { useLibrary } from "@/lib/useLibrary";
import type { components } from "@/api/schema";

import { isTierVisible, useTier } from "@/lib/useTier";

import { Message, Toggle, inputStyle } from "./LibraryNaming";
import { SaveBar, SettingRow } from "./SettingsCard";

type FileManagement = components["schemas"]["FileManagement"];

/** Sonarr's Folders, File Management and Permissions sections, for one library (2026-09-19). */
export function LibraryFileManagement() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const queryClient = useQueryClient();
  // Sonarr's basic view: folders and deleted tracks; the recycle bin and the rest are advanced (2026-09-30).
  const advanced = isTierVisible(useTier(), "advanced");
  const isTV = useLibrary(categoryId ?? "")?.media_type === "tv";

  const saved = useQuery({
    queryKey: ["libraryFileManagement", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/file-management", { params: { path: { id: id! } } });
      if (error) throw error;
      return data;
    },
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
        {!isTV && (
          <Toggle
            label="Create Empty Artist Folders"
            help="Create missing artist folders for monitored artists during disk scan."
            checked={draft.create_empty_artist_folders}
            onChange={(checked) => set({ create_empty_artist_folders: checked })}
          />
        )}
        <Toggle
          label="Delete Empty Folders"
          help={
            isTV
              ? "Delete a series or season folder left empty when episode files are deleted."
              : "Delete empty artist and album folders during disk scan and when track files are deleted."
          }
          checked={draft.delete_empty_folders}
          onChange={(checked) => set({ delete_empty_folders: checked })}
        />
      </section>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">File Management</h2>
        <Toggle
          label={isTV ? "Unmonitor Deleted Episodes" : "Unmonitor Deleted Tracks"}
          help={
            isTV
              ? "An episode whose file is deleted from disk is unmonitored instead of being fetched again."
              : "An album with a track deleted from disk is automatically unmonitored."
          }
          checked={draft.unmonitor_deleted}
          onChange={(checked) => set({ unmonitor_deleted: checked })}
        />
        {!isTV && (
        <Toggle
          label="Merge Artists Stored Under Several Names"
          help="After each disk scan, an artist found under several folder names or aliases becomes one artist: the identified artist wins, otherwise the name with the most albums. Files stay where they are."
          checked={draft.merge_artist_aliases}
          onChange={(checked) => set({ merge_artist_aliases: checked })}
        />
        )}
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
        {!isTV && (
        <Choice
          label="Rescan Artist Folder after Refresh"
          help={
            draft.rescan_after_refresh === "always"
              ? "Rescan the artist's folders after refreshing its metadata."
              : "Changes to files on disk are not noticed after a refresh unless this is Always."
          }
          value={draft.rescan_after_refresh}
          options={[
            ["always", "Always"],
            ["after_manual", "Only After Manual Refresh"],
            ["never", "Never"],
          ]}
          onChange={(value) => set({ rescan_after_refresh: value as FileManagement["rescan_after_refresh"] })}
        />
        )}
        <Choice
          label="Change File Date"
          help="Change the file date on import and rescan."
          value={draft.change_file_date}
          options={[
            ["none", "None"],
            ["release_date", isTV ? "Episode Air Date" : "Album Release Date"],
          ]}
          onChange={(value) => set({ change_file_date: value as FileManagement["change_file_date"] })}
        />
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

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Importing</h2>
        <Toggle
          label="Import Extra Files"
          help={
            isTV
              ? "Bring files named after an episode along with it, such as its subtitles, under the episode's new name."
              : "Bring matching files along with the tracks: lyrics, cue sheets, rip logs."
          }
          checked={draft.import_extra_files ?? false}
          onChange={(checked) => set({ import_extra_files: checked })}
        />
        {draft.import_extra_files && (
          <SettingRow
            label="Extra File Extensions"
            help={
              isTV
                ? "Comma separated, such as srt,ass. A subtitle keeps what follows the episode's name (.en.srt)."
                : "Comma separated, such as lrc,cue,log,nfo. A file named like a track takes the track's new name; others keep theirs."
            }
          >
            <input
              value={(draft.extra_file_extensions ?? []).join(",")}
              onChange={(event) => set({ extra_file_extensions: event.target.value.split(",") })}
              className={`${inputStyle} w-44 font-mono`}
            />
          </SettingRow>
        )}
      </section>

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
