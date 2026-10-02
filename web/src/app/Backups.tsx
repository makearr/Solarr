import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { API_PREFIX, api } from "@/api/client";
import type { components } from "@/api/schema";

import { Card } from "./SettingsCard";

type ImportWarning = components["schemas"]["SettingsImportWarning"];

/** ADR-15's database backups, read from disk, and the settings document (F9) to download and
 *  restore. A database restore stays the documented offline procedure. */
export function Backups() {
  const queryClient = useQueryClient();

  const files = useQuery({
    queryKey: ["backups"],
    queryFn: async () => {
      const { data, error } = await api.GET("/system/backups");
      if (error) throw error;
      return data;
    },
  });

  const runNow = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/system/backups", {});
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["backups"] }),
  });

  const groups = groupByDatabase(files.data ?? []);

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-4 p-6">
      <h1 className="text-lg font-semibold">Backups</h1>
      <SettingsDocument />

      <Card
        title="Database backups"
        help="Taken automatically, and before every migration. Restoring a database is an offline procedure: stop the server and put the file back. There is no button for it on purpose."
        toggle={
          <button
            type="button"
            disabled={runNow.isPending}
            onClick={() => runNow.mutate()}
            className="shrink-0 rounded-md border border-border-subtle px-3 py-1 text-xs text-ink-muted enabled:hover:text-ink disabled:opacity-50"
          >
            {runNow.isPending ? "Backing up…" : "Back up now"}
          </button>
        }
      >
        {files.isLoading && <Message>Loading…</Message>}
        {files.error !== null && <Message>The backup directory could not be read.</Message>}
        {files.data && files.data.length === 0 && <Message>No backups yet.</Message>}
        {groups.map((group) => (
          <BackupGroup key={group.database} group={group} />
        ))}
        {runNow.isError && <p className="text-xs text-danger">The backup did not complete.</p>}
      </Card>
    </div>
  );
}

type BackupFile = components["schemas"]["BackupFile"];

/** The database a backup file belongs to: its name up to ".db" ("library.db.rotation.2026-…" is library). */
function groupByDatabase(files: readonly BackupFile[]): { database: string; files: BackupFile[] }[] {
  const groups = new Map<string, BackupFile[]>();
  for (const file of files) {
    const database = file.name.includes(".db") ? file.name.slice(0, file.name.indexOf(".db")) : "other";
    groups.set(database, [...(groups.get(database) ?? []), file]);
  }
  return [...groups.entries()].map(([database, grouped]) => ({ database, files: grouped }));
}

const newestShown = 4;

function BackupGroup({ group }: { group: { database: string; files: BackupFile[] } }) {
  const [all, setAll] = useState(false);
  const shown = all ? group.files : group.files.slice(0, newestShown);
  return (
    <div className="flex flex-col gap-1.5 border-t border-border-subtle pt-3 first:border-t-0 first:pt-0">
      <div className="flex items-baseline gap-2">
        <h3 className="text-sm font-medium">{group.database}</h3>
        <span className="text-xs text-ink-muted">
          {group.files.length} {group.files.length === 1 ? "backup" : "backups"}, newest {new Date(group.files[0]!.taken_at).toLocaleString()}
        </span>
      </div>
      <ul className="flex flex-col gap-1">
        {shown.map((file) => (
          <li key={file.name} className="flex items-center gap-3 rounded-md border border-border-subtle px-3 py-1.5 text-xs">
            <span className="min-w-0 flex-1 truncate font-mono">{file.name}</span>
            {file.pre_migration === true && <span className="shrink-0 text-ink-muted">before a migration</span>}
            <span className="shrink-0 text-ink-muted">{formatSize(file.size_bytes)}</span>
            <span className="shrink-0 text-ink-muted">{new Date(file.taken_at).toLocaleString()}</span>
          </li>
        ))}
      </ul>
      {group.files.length > newestShown && (
        <button type="button" onClick={() => setAll(!all)} className="self-start text-xs text-accent hover:underline">
          {all ? "Show fewer" : `Show all ${group.files.length}`}
        </button>
      )}
    </div>
  );
}

/** Download the settings as YAML, or restore a document: it merges on top of what is there and
 *  applies entirely or not at all, every problem reported at once. */
function SettingsDocument() {
  const queryClient = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [chosen, setChosen] = useState<File | null>(null);

  const download = useMutation({
    mutationFn: async () => {
      const response = await fetch(`${API_PREFIX}/settings/export`, { credentials: "same-origin" });
      if (!response.ok) throw new Error(`export answered ${response.status}`);
      const link = document.createElement("a");
      link.href = URL.createObjectURL(await response.blob());
      link.download = `solarr-settings-${new Date().toISOString().slice(0, 10)}.yml`;
      link.click();
      URL.revokeObjectURL(link.href);
    },
  });

  const restore = useMutation({
    mutationFn: async (file: File): Promise<ImportWarning[]> => {
      const response = await fetch(`${API_PREFIX}/settings/import`, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/yaml" },
        body: await file.text(),
      });
      const body = (await response.json()) as { warnings?: ImportWarning[]; detail?: string };
      if (!response.ok) throw new Error(body.detail ?? `restore answered ${response.status}`);
      return body.warnings ?? [];
    },
    onSuccess: () => {
      setChosen(null);
      if (input.current) input.current.value = "";
      void queryClient.invalidateQueries();
    },
  });

  return (
    <Card
      title="Settings file"
      help="Libraries, root folders, profiles, naming, schedules and every setting, as one file. Accounts and API keys are never in it."
    >
      <div className="flex flex-wrap items-center gap-2 text-xs">
        <button
          type="button"
          disabled={download.isPending}
          onClick={() => download.mutate()}
          className="rounded-md border border-border-subtle px-3 py-1 text-ink-muted enabled:hover:text-ink disabled:opacity-50"
        >
          Download settings
        </button>
        <label className="flex items-center gap-2">
          <span className="text-ink">Restore from</span>
          <input
            ref={input}
            type="file"
            accept=".yml,.yaml,application/yaml,text/yaml"
            onChange={(event) => {
              restore.reset();
              setChosen(event.target.files?.[0] ?? null);
            }}
            className="text-ink-muted file:mr-2 file:rounded-md file:border file:border-border-subtle
                       file:bg-canvas file:px-2 file:py-1 file:text-ink"
          />
        </label>
      </div>
      {chosen && (
        <div className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle p-2">
          <span className="text-ink-muted">
            Apply {chosen.name} on top of the current settings? Nothing is deleted, and nothing changes
            unless all of it is valid.
          </span>
          <button
            type="button"
            disabled={restore.isPending}
            onClick={() => restore.mutate(chosen)}
            className="rounded-md bg-accent px-3 py-1 text-accent-ink disabled:opacity-50"
          >
            {restore.isPending ? "Restoring…" : "Restore"}
          </button>
        </div>
      )}
      {restore.isSuccess && (
        <div className="flex flex-col gap-1">
          <p className="text-ink">Restored.</p>
          {restore.data.length > 0 && (
            <ul className="flex flex-col gap-0.5 text-warn">
              {restore.data.map((warning) => (
                <li key={warning.path}>
                  <span className="font-mono">{warning.path}</span>: {warning.message}
                  {warning.suggestion && ` (did you mean ${warning.suggestion}?)`}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {restore.isError && <pre className="whitespace-pre-wrap font-mono text-danger">{restore.error.message}</pre>}
      {download.isError && <p className="text-xs text-danger">The settings could not be downloaded.</p>}
    </Card>
  );
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB"];
  let value = bytes / 1024;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value.toFixed(1)} ${units[unit]}`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="text-sm text-ink-muted">{children}</p>;
}
