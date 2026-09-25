import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Downloader = components["schemas"]["Downloader"];
type Protocol = components["schemas"]["Downloader"]["protocol"];
type ConnectionTestResult = components["schemas"]["ConnectionTestResult"];

const PROTOCOLS: readonly Protocol[] = ["usenet", "torrent", "ddl"];

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

function AddDownloader({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("");
  const [impl, setImpl] = useState("sabnzbd");
  const [baseUrl, setBaseUrl] = useState("");
  const [protocol, setProtocol] = useState<Protocol>("usenet");
  const [credentials, setCredentials] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/downloaders", {
        body: { name, impl, base_url: baseUrl, protocol, credentials: credentials || undefined, enabled: true },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setName("");
      setBaseUrl("");
      setCredentials("");
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
        Type
        <input
          className={inputStyle}
          value={impl}
          placeholder="sabnzbd"
          autoComplete="off"
          onChange={(event) => setImpl(event.target.value)}
        />
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          placeholder="SABnzbd"
          autoComplete="off"
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Base URL
        <input
          className={inputStyle}
          value={baseUrl}
          placeholder="http://sabnzbd:8080"
          autoComplete="off"
          onChange={(event) => setBaseUrl(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Protocol
        <select className={inputStyle} value={protocol} onChange={(event) => setProtocol(event.target.value as Protocol)}>
          {PROTOCOLS.map((value) => (
            <option key={value} value={value}>
              {value}
            </option>
          ))}
        </select>
      </label>
      <label className="flex min-w-40 flex-col gap-1 text-xs text-ink-muted">
        API key
        <input
          className={inputStyle}
          type="password"
          value={credentials}
          autoComplete="off"
          onChange={(event) => setCredentials(event.target.value)}
        />
      </label>
      <button
        type="submit"
        disabled={!name.trim() || !baseUrl.trim() || create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Connecting…" : "Connect"}
      </button>
      {create.isError && <p className="w-full text-xs text-danger">That connection could not be created.</p>}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
