import { useEffect, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { isTierVisible, useTier } from "@/lib/useTier";

import { Message, Toggle, inputStyle } from "./LibraryNaming";

type FileManagement = components["schemas"]["FileManagement"];

/** Sonarr's Folders, File Management and Permissions sections, for one library (2026-09-19). */
export function LibraryFileManagement() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const queryClient = useQueryClient();
  // Sonarr's basic view: folders and deleted tracks; the recycle bin and the rest are advanced (2026-09-30).
  const advanced = isTierVisible(useTier(), "advanced");

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
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-5 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">File management</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          What Solarr does with this library&apos;s folders and files, as in Sonarr&apos;s Media Management.
        </p>
      </header>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">Folders</h2>
        <Toggle
          label="Create Empty Artist Folders"
          help="Create missing artist folders for monitored artists during disk scan."
          checked={draft.create_empty_artist_folders}
          onChange={(checked) => set({ create_empty_artist_folders: checked })}
        />
        <Toggle
          label="Delete Empty Folders"
          help="Delete empty artist and album folders during disk scan and when track files are deleted."
          checked={draft.delete_empty_folders}
          onChange={(checked) => set({ delete_empty_folders: checked })}
        />
      </section>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <h2 className="text-sm font-medium">File Management</h2>
        <Toggle
          label="Unmonitor Deleted Tracks"
          help="An album with a track deleted from disk is automatically unmonitored."
          checked={draft.unmonitor_deleted}
          onChange={(checked) => set({ unmonitor_deleted: checked })}
        />
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
        <Choice
          label="Change File Date"
          help="Change the file date on import and rescan."
          value={draft.change_file_date}
          options={[
            ["none", "None"],
            ["release_date", "Album Release Date"],
          ]}
          onChange={(value) => set({ change_file_date: value as FileManagement["change_file_date"] })}
        />
        </>
        )}
        {advanced && (
        <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <span className="w-48 shrink-0">Recycling Bin Cleanup</span>
          <input
            type="number"
            min={1}
            max={365}
            value={draft.recycle_retention_days}
            onChange={(event) => set({ recycle_retention_days: Number(event.target.value) || 1 })}
            className={`${inputStyle} w-20`}
          />
          <span className="text-xs text-ink-muted">
            days, for this library. Deleted files go to each root folder&apos;s recycle bin, never straight to
            deletion, and are removed after this -- or sooner once the bin passes its size limit under
            Storage.
          </span>
        </label>
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
            <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span className="w-48 shrink-0">chmod Folder</span>
              <input
                value={draft.chmod_folder}
                onChange={(event) => set({ chmod_folder: event.target.value })}
                className={`${inputStyle} w-20 font-mono`}
              />
              <span className="text-xs text-ink-muted">Octal. Files get the same without the execute bits.</span>
            </label>
            <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
              <span className="w-48 shrink-0">chown Group</span>
              <input
                value={draft.chown_group}
                placeholder="unchanged"
                onChange={(event) => set({ chown_group: event.target.value })}
                className={`${inputStyle} w-32 font-mono`}
              />
              <span className="text-xs text-ink-muted">Group name or gid. Use the gid for network filesystems.</span>
            </label>
          </>
        )}
      </section>
      )}

      <div className="flex items-center gap-2">
        <button
          type="button"
          disabled={!dirty || save.isPending}
          onClick={() => save.mutate(draft)}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
        {dirty && (
          <button
            type="button"
            onClick={() => setDraft(saved.data)}
            className="rounded-md border border-border-subtle px-3 py-1.5 text-sm hover:text-ink"
          >
            Undo
          </button>
        )}
        {save.isError && <span className="text-xs text-danger">{save.error.message}</span>}
      </div>
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
    <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
      <span className="w-48 shrink-0">{label}</span>
      <select value={value} onChange={(event) => onChange(event.target.value)} className={inputStyle}>
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </select>
      <span className="text-xs text-ink-muted">{help}</span>
    </label>
  );
}
