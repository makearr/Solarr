import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, RefreshCw, RotateCcw, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type ArrInstance = components["schemas"]["ArrInstance"];
type ArrApp = components["schemas"]["ArrApp"];
type ArrImportRun = components["schemas"]["ArrImportRun"];

const APPS: readonly ArrApp[] = ["lidarr", "sonarr", "radarr", "readarr", "whisparr"];

/** Todo 4.13, ADR-52: import from the *Arr applications over their own REST
 *  API. Only Lidarr has a real sync so far -- a monitored artist becomes a
 *  person + subscription, a monitored album becomes a desired_item, each
 *  matched by its own MusicBrainz id. The other four apps can still be
 *  connected, ready for when their own stage ships. */
export function ArrImport() {
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<number | null>(null);

  const instances = useQuery({
    queryKey: ["arr-instances"],
    queryFn: async () => {
      const { data, error } = await api.GET("/arr/instances");
      if (error) throw error;
      return data;
    },
  });

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["arr-instances"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/arr/instances/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">*Arr import</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Connect an existing Sonarr/Radarr/Lidarr/Readarr/Whisparr and bring in what it already
          monitors as intent -- never a download, never a write back to the source. Only Lidarr
          has a real sync today; the others connect and wait for their own stage.
        </p>
      </header>

      {instances.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {instances.error !== null && <p className="text-sm text-danger">Connections could not be read.</p>}

      {instances.data && instances.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {instances.data.map((instance) => (
            <InstanceCard
              key={instance.id}
              instance={instance}
              categoryName={categories.data?.find((c) => c.id === instance.category_id)?.name}
              expanded={expanded === instance.id}
              onToggleExpanded={() => setExpanded(expanded === instance.id ? null : instance.id)}
              onRemove={() => remove.mutate(instance.id)}
              onChanged={invalidate}
              busy={remove.isPending}
            />
          ))}
        </ul>
      ) : (
        !instances.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No connection is configured.
          </p>
        )
      )}

      {remove.isError && <p className="text-xs text-danger">That connection could not be removed.</p>}

      <AddInstance categories={categories.data ?? []} onAdded={invalidate} />
    </div>
  );
}

function InstanceCard({
  instance,
  categoryName,
  expanded,
  onToggleExpanded,
  onRemove,
  onChanged,
  busy,
}: {
  instance: ArrInstance;
  categoryName: string | undefined;
  expanded: boolean;
  onToggleExpanded: () => void;
  onRemove: () => void;
  onChanged: () => void;
  busy: boolean;
}) {
  const queryClient = useQueryClient();

  const sync = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/arr/instances/{id}/sync", {
        params: { path: { id: instance.id } },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      onChanged();
      void queryClient.invalidateQueries({ queryKey: ["arr-imports", instance.id] });
    },
  });

  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-label={expanded ? "Collapse history" : "Show history"}
          onClick={onToggleExpanded}
          className="p-0.5 text-ink-muted hover:text-ink focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        </button>
        <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem]
                         uppercase text-ink-muted">
          {instance.app}
        </span>
        <span className="font-medium">{instance.name}</span>
        <span className="truncate font-mono text-xs text-ink-muted">{instance.base_url}</span>
        {categoryName && (
          <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
            {categoryName}
          </span>
        )}
        {!instance.enabled && (
          <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
            disabled
          </span>
        )}
        {instance.last_sync_error && (
          <span
            className="rounded-full border border-danger px-1.5 py-px text-[0.6875rem] text-danger"
            title={instance.last_sync_error}
          >
            last sync failed
          </span>
        )}

        <button
          type="button"
          onClick={() => sync.mutate()}
          disabled={sync.isPending}
          className="ml-auto flex shrink-0 items-center gap-1 rounded-md border border-border-subtle
                     px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <RefreshCw aria-hidden className={`size-3.5 ${sync.isPending ? "animate-spin" : ""}`} />
          {sync.isPending ? "Syncing…" : "Sync now"}
        </button>
        <button
          type="button"
          aria-label={`Remove ${instance.name}`}
          onClick={onRemove}
          disabled={busy}
          className="shrink-0 rounded-md border border-border-subtle p-1 text-ink-muted
                     hover:text-danger disabled:opacity-50 focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <Trash2 aria-hidden className="size-3.5" />
        </button>
      </div>

      {sync.isError && <p className="text-xs text-danger">The sync could not run -- check the connection.</p>}
      {sync.data && (
        <RunSummary run={sync.data} />
      )}

      {expanded && <InstanceHistory instanceId={instance.id} />}
    </li>
  );
}

function RunSummary({ run }: { run: ArrImportRun }) {
  return (
    <p className="rounded-md bg-canvas px-2 py-1.5 text-xs text-ink-muted">
      {run.state === "DONE" ? (
        <>
          {run.artists_created} artist{run.artists_created === 1 ? "" : "s"} added,{" "}
          {run.artists_matched} matched · {run.albums_created} album{run.albums_created === 1 ? "" : "s"} added,{" "}
          {run.albums_matched} matched
        </>
      ) : (
        <span className="text-danger">{run.state}</span>
      )}
      {run.error && <span className="block text-danger">{run.error}</span>}
    </p>
  );
}

function InstanceHistory({ instanceId }: { instanceId: number }) {
  const queryClient = useQueryClient();
  const runs = useQuery({
    queryKey: ["arr-imports", instanceId],
    queryFn: async () => {
      const { data, error } = await api.GET("/arr/imports", { params: { query: { instance_id: instanceId } } });
      if (error) throw error;
      return data;
    },
  });

  const revert = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.POST("/arr/imports/{id}/revert", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["arr-imports", instanceId] }),
  });

  return (
    <div className="border-t border-border-subtle pt-2">
      {runs.isLoading && <p className="text-xs text-ink-muted">Reading…</p>}
      {runs.error !== null && <p className="text-xs text-danger">History could not be read.</p>}
      {runs.data && runs.data.length === 0 && <p className="text-xs text-ink-muted">No run yet.</p>}
      {runs.data && runs.data.length > 0 && (
        <ul className="flex flex-col gap-1">
          {runs.data.map((run) => (
            <li key={run.id} className="flex items-center gap-2 text-xs text-ink-muted">
              <span className="w-16 shrink-0">{run.state}</span>
              <span>{new Date(run.started_at).toLocaleString()}</span>
              <span className="ml-auto">
                {run.artists_created + run.albums_created} created, {run.artists_matched + run.albums_matched} matched
              </span>
              {run.state === "DONE" && (
                <button
                  type="button"
                  aria-label={`Revert run ${run.id}`}
                  onClick={() => revert.mutate(run.id)}
                  disabled={revert.isPending}
                  className="shrink-0 rounded-md border border-border-subtle p-1 text-ink-muted
                             hover:text-danger disabled:opacity-50 focus-visible:outline-2
                             focus-visible:outline-offset-2 focus-visible:outline-accent"
                >
                  <RotateCcw aria-hidden className="size-3" />
                </button>
              )}
              {run.state === "REVERTED" && <span className="shrink-0">reverted</span>}
            </li>
          ))}
        </ul>
      )}
      {revert.isError && <p className="text-xs text-danger">That run could not be reverted.</p>}
    </div>
  );
}

function AddInstance({
  categories,
  onAdded,
}: {
  categories: readonly { id: number; name: string }[];
  onAdded: () => void;
}) {
  const [app, setApp] = useState<ArrApp>("lidarr");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [categoryId, setCategoryId] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/arr/instances", {
        body: {
          app,
          name,
          base_url: baseUrl,
          api_key: apiKey,
          category_id: categoryId ? Number(categoryId) : undefined,
          enabled: true,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setName("");
      setBaseUrl("");
      setApiKey("");
      setCategoryId("");
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        App
        <select className={inputStyle} value={app} onChange={(event) => setApp(event.target.value as ArrApp)}>
          {APPS.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          placeholder="Home Lidarr"
          autoComplete="off"
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Base URL
        <input
          className={inputStyle}
          value={baseUrl}
          placeholder="http://lidarr:8686"
          autoComplete="off"
          onChange={(event) => setBaseUrl(event.target.value)}
        />
      </label>
      <label className="flex min-w-40 flex-col gap-1 text-xs text-ink-muted">
        API key
        <input
          className={inputStyle}
          type="password"
          value={apiKey}
          autoComplete="off"
          onChange={(event) => setApiKey(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Category
        <select className={inputStyle} value={categoryId} onChange={(event) => setCategoryId(event.target.value)}>
          <option value="">Match by root folder path</option>
          {categories.map((category) => (
            <option key={category.id} value={category.id}>
              {category.name}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={!name.trim() || !baseUrl.trim() || !apiKey.trim() || create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Connecting…" : "Connect"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">That connection could not be created.</p>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
