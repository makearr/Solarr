import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Downloader = components["schemas"]["Downloader"];

type ConnectionTestResult = components["schemas"]["ConnectionTestResult"];


/** Stage 5, ADR-46/57: download clients. SABnzbd is the only implementation
 *  wired up today; more protocols/clients arrive with their own stage.
 *  Actually dispatching a download waits on the candidate pipeline -- this
 *  screen manages the connection record and can test it, nothing more. */
export function Downloaders() {
  const queryClient = useQueryClient();

  const downloaders = useQuery({
    queryKey: ["downloaders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/downloaders");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["downloaders"] });

  const setEnabled = useMutation({
    mutationFn: async (args: { downloader: Downloader; enabled: boolean }) => {
      const { error } = await api.PATCH("/downloaders/{id}", {
        params: { path: { id: args.downloader.id } },
        body: {
          name: args.downloader.name, impl: args.downloader.impl, base_url: args.downloader.base_url,
          protocol: args.downloader.protocol, enabled: args.enabled,
        },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/downloaders/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const [editing, setEditing] = useState<number | null>(null);
  const [testResults, setTestResults] = useState<Record<number, ConnectionTestResult>>({});
  const test = useMutation({
    mutationFn: async (id: number) => {
      const { data, error } = await api.POST("/downloaders/{id}/test", { params: { path: { id } } });
      if (error) throw error;
      return { id, result: data! };
    },
    onSuccess: ({ id, result }) => setTestResults((prev) => ({ ...prev, [id]: result })),
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Download clients</h1>
      </header>

      {downloaders.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {downloaders.error !== null && <p className="text-sm text-danger">Download clients could not be read.</p>}

      {downloaders.data && downloaders.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {downloaders.data.map((downloader) => (
            <li
              key={downloader.id}
              className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{downloader.name}</span>
                <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
                  {downloader.impl}
                </span>
                <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
                  {downloader.protocol}
                </span>
                <span className="truncate font-mono text-xs text-ink-muted">{downloader.base_url}</span>
                <span className="ml-auto flex shrink-0 items-center gap-2 text-xs">
                  <span
                    className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
                      downloader.credentials.has_value ? "border-border-subtle text-ink-muted" : "border-warn text-warn"
                    }`}
                  >
                    {downloader.credentials.has_value ? "credentials set" : "no credentials"}
                  </span>
                  <label className="flex items-center gap-1 text-ink-muted">
                    <input
                      type="checkbox"
                      checked={downloader.enabled}
                      disabled={setEnabled.isPending}
                      onChange={(event) => setEnabled.mutate({ downloader, enabled: event.target.checked })}
                    />
                    enabled
                  </label>
                  <button
                    type="button"
                    onClick={() => test.mutate(downloader.id)}
                    disabled={test.isPending}
                    className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink disabled:opacity-50"
                  >
                    {test.isPending && test.variables === downloader.id ? "Testing…" : "Test"}
                  </button>
                  <button
                    type="button"
                    aria-label={`Remove ${downloader.name}`}
                    onClick={() => remove.mutate(downloader.id)}
                    disabled={remove.isPending}
                    className="rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-50"
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                </span>
              </div>
              <span className="flex items-center gap-2 text-xs text-ink-muted">
                {describeOptions(downloader.options)}
                <button
                  type="button"
                  onClick={() => setEditing(editing === downloader.id ? null : downloader.id)}
                  className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink"
                >
                  Options
                </button>
              </span>
              {editing === downloader.id && (
                <ClientOptionsForm
                  downloader={downloader}
                  onDone={() => {
                    setEditing(null);
                    void invalidate();
                  }}
                />
              )}
              {(() => {
                const result = testResults[downloader.id];
                return result && (
                  <span className={`text-xs ${result.ok ? "text-ok" : "text-danger"}`}>{result.message}</span>
                );
              })()}
            </li>
          ))}
        </ul>
      ) : (
        !downloaders.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No download client is configured.
          </p>
        )
      )}

      {(setEnabled.isError || remove.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddDownloader onAdded={invalidate} />
    </div>
  );
}

type DownloaderOptions = components["schemas"]["DownloaderOptions"];

/** Lidarr's add dialog: a name already filled in, "Test" before "Add", and SABnzbd's own download
 *  folder offered as a download folder, at the path Solarr sees it if that differs. */
function AddDownloader({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("SABnzbd");
  const [baseUrl, setBaseUrl] = useState("");
  const [credentials, setCredentials] = useState("");
  const [category, setCategory] = useState("");
  const [removeCompleted, setRemoveCompleted] = useState(true);
  const [maxQueued, setMaxQueued] = useState(DEFAULT_MAX_QUEUED);
  const [addFolder, setAddFolder] = useState(true);
  const [localFolder, setLocalFolder] = useState("");

  const test = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/downloaders/test", {
        body: { impl: "sabnzbd", base_url: baseUrl, credentials: credentials || undefined, options: { category: category || undefined } },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (result) => setLocalFolder(result.download_folder ?? ""),
  });
  const reported = test.data?.ok ? (test.data.download_folder ?? "") : "";

  const create = useMutation({
    mutationFn: async () => {
      // A folder the client names differently from Solarr is Lidarr's remote path mapping.
      const mapped = reported !== "" && localFolder !== "" && localFolder !== reported;
      const options: DownloaderOptions = {
        category: category || undefined,
        remove_completed: removeCompleted,
        max_queued: maxQueued,
        remote_path: mapped ? reported : undefined,
        local_path: mapped ? localFolder : undefined,
      };
      const { error } = await api.POST("/downloaders", {
        body: { name, impl: "sabnzbd", base_url: baseUrl, protocol: "usenet", credentials: credentials || undefined, enabled: true, options },
      });
      if (error) throw error;
      if (addFolder && localFolder) {
        const added = await api.POST("/downloadfolders", { body: { path: localFolder, label: name, watch: false } });
        if (added.error) throw new Error(`The client was added, but the download folder was not: ${(added.error as { detail?: string }).detail ?? ""}`);
      }
    },
    onSuccess: () => {
      setBaseUrl("");
      setCredentials("");
      test.reset();
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <h2 className="text-sm font-medium">Add SABnzbd</h2>
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
          Name
          <input className={inputStyle} value={name} autoComplete="off" onChange={(event) => setName(event.target.value)} />
        </label>
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
          Address
          <input
            className={inputStyle}
            value={baseUrl}
            placeholder="http://sabnzbd:8080"
            autoComplete="off"
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </label>
        <label className="flex min-w-40 flex-col gap-1 text-xs text-ink-muted">
          API key
          <input className={inputStyle} type="password" value={credentials} autoComplete="off" onChange={(event) => setCredentials(event.target.value)} />
        </label>
      </div>
      <OptionFields
        category={category}
        onCategory={setCategory}
        removeCompleted={removeCompleted}
        onRemoveCompleted={setRemoveCompleted}
        maxQueued={maxQueued}
        onMaxQueued={setMaxQueued}
      />
      {test.data && (
        <p className={`text-xs ${test.data.ok ? "text-ok" : "text-danger"}`}>{test.data.message}</p>
      )}
      {test.isError && <p className="text-xs text-danger">The test could not run.</p>}
      {reported !== "" && (
        <div className="flex flex-wrap items-end gap-2 text-xs">
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={addFolder} onChange={(event) => setAddFolder(event.target.checked)} />
            Also add its download folder
          </label>
          {addFolder && (
            <label className="flex min-w-64 flex-1 flex-col gap-1 text-ink-muted">
              Solarr sees {reported} at
              <input className={inputStyle} value={localFolder} onChange={(event) => setLocalFolder(event.target.value)} />
            </label>
          )}
        </div>
      )}
      <div className="flex gap-2">
        <button
          type="button"
          disabled={!baseUrl.trim() || test.isPending}
          onClick={() => test.mutate()}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink disabled:opacity-50"
        >
          {test.isPending ? "Testing…" : "Test"}
        </button>
        <button
          type="submit"
          disabled={!name.trim() || !baseUrl.trim() || create.isPending}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {create.isPending ? "Adding…" : "Add"}
        </button>
      </div>
      {create.isError && (
        <p className="text-xs text-danger">{(create.error as Error).message || "The client could not be added."}</p>
      )}
    </form>
  );
}

// ADR-57's default: once sent, the client decides the order, so Solarr sends only a few at a time.
const DEFAULT_MAX_QUEUED = 10;

function OptionFields({
  category,
  onCategory,
  removeCompleted,
  onRemoveCompleted,
  maxQueued,
  onMaxQueued,
}: {
  category: string;
  onCategory: (value: string) => void;
  removeCompleted: boolean;
  onRemoveCompleted: (value: boolean) => void;
  maxQueued: number;
  onMaxQueued: (value: number) => void;
}) {
  return (
    <div className="flex flex-wrap items-end gap-3 text-xs">
      <label className="flex min-w-32 flex-col gap-1 text-ink-muted">
        Category
        <input className={inputStyle} value={category} placeholder="music" onChange={(event) => onCategory(event.target.value)} />
      </label>
      <label className="flex items-center gap-2 pb-2 text-ink-muted">
        <input type="checkbox" checked={removeCompleted} onChange={(event) => onRemoveCompleted(event.target.checked)} />
        Remove completed downloads once imported
      </label>
      <label
        className="flex w-36 flex-col gap-1 text-ink-muted"
        title="Searches beyond this wait in Solarr, best first, and go as downloads finish"
      >
        Downloads at once
        <input
          className={inputStyle}
          type="number"
          min={1}
          max={1000}
          value={maxQueued}
          onChange={(event) => onMaxQueued(Math.max(1, Math.min(1000, Number(event.target.value) || 1)))}
        />
      </label>
    </div>
  );
}

/** An existing client's options, as Lidarr's client dialog edits them. */
function ClientOptionsForm({ downloader, onDone }: { downloader: Downloader; onDone: () => void }) {
  const current = downloader.options ?? {};
  const [category, setCategory] = useState(current.category ?? "");
  const [removeCompleted, setRemoveCompleted] = useState(current.remove_completed ?? true);
  const [remotePath, setRemotePath] = useState(current.remote_path ?? "");
  const [localPath, setLocalPath] = useState(current.local_path ?? "");
  const [maxQueued, setMaxQueued] = useState(current.max_queued ?? DEFAULT_MAX_QUEUED);
  const save = useMutation({
    mutationFn: async () => {
      const { error } = await api.PATCH("/downloaders/{id}", {
        params: { path: { id: downloader.id } },
        body: {
          name: downloader.name, impl: downloader.impl, base_url: downloader.base_url, protocol: downloader.protocol,
          enabled: downloader.enabled,
          options: {
            category: category || undefined, remove_completed: removeCompleted, max_queued: maxQueued,
            remote_path: remotePath || undefined, local_path: localPath || undefined,
          },
        },
      });
      if (error) throw error;
    },
    onSuccess: onDone,
  });
  return (
    <div className="flex flex-col gap-2 border-t border-border-subtle pt-2">
      <OptionFields
        category={category}
        onCategory={setCategory}
        removeCompleted={removeCompleted}
        onRemoveCompleted={setRemoveCompleted}
        maxQueued={maxQueued}
        onMaxQueued={setMaxQueued}
      />
      <div className="flex flex-wrap items-end gap-3 text-xs">
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-ink-muted">
          The client says its downloads are in
          <input className={inputStyle} value={remotePath} placeholder="/downloads" onChange={(event) => setRemotePath(event.target.value)} />
        </label>
        <label className="flex min-w-48 flex-1 flex-col gap-1 text-ink-muted">
          Solarr sees that folder at
          <input className={inputStyle} value={localPath} placeholder="/data/downloads" onChange={(event) => setLocalPath(event.target.value)} />
        </label>
      </div>
      <button
        type="button"
        disabled={save.isPending}
        onClick={() => save.mutate()}
        className="w-fit rounded-md bg-accent px-3 py-1 text-xs text-accent-ink disabled:opacity-50"
      >
        Save
      </button>
      {save.isError && <p className="text-xs text-danger">The options could not be saved.</p>}
    </div>
  );
}

function describeOptions(options: DownloaderOptions | undefined): string {
  const parts = [`category ${options?.category || "music"}`];
  parts.push(options?.remove_completed === false ? "keeps completed downloads" : "removes completed downloads");
  if (options?.remote_path && options.local_path) parts.push(`${options.remote_path} is ${options.local_path} here`);
  return parts.join(" · ");
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
