import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Indexer = components["schemas"]["Indexer"];

/** Stage 5, ADR-37/43/47: Newznab/Torznab search providers. A public
 *  Torznab endpoint may need no API key at all -- left blank, the
 *  connection is created with none rather than refused. Connection
 *  testing (todo 5.2's own "every connection tested before it is
 *  accepted") is not built yet; a bad URL or key surfaces only once a
 *  real search runs against it (stage 5's own candidate pipeline, not
 *  built yet either). */
export function Indexers() {
  const queryClient = useQueryClient();

  const indexers = useQuery({
    queryKey: ["indexers"],
    queryFn: async () => {
      const { data, error } = await api.GET("/indexers");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["indexers"] });

  const setEnabled = useMutation({
    mutationFn: async (args: { indexer: Indexer; enabled: boolean }) => {
      const { error } = await api.PATCH("/indexers/{id}", {
        params: { path: { id: args.indexer.id } },
        body: {
          name: args.indexer.name, impl: args.indexer.impl, base_url: args.indexer.base_url,
          priority: args.indexer.priority, enabled: args.enabled,
        },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/indexers/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Indexers</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Newznab and Torznab search providers, spoken natively (Prowlarr works through this
          unmodified) -- no Cardigann, no scraping definitions.
        </p>
      </header>

      {indexers.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {indexers.error !== null && <p className="text-sm text-danger">Indexers could not be read.</p>}

      {indexers.data && indexers.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {indexers.data.map((indexer) => (
            <li
              key={indexer.id}
              className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3"
            >
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-medium">{indexer.name}</span>
                <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
                  {indexer.impl}
                </span>
                <span className="truncate font-mono text-xs text-ink-muted">{indexer.base_url}</span>
                <span className="ml-auto flex shrink-0 items-center gap-2 text-xs">
                  <Badge tone={indexer.api_key.has_value ? "ok" : "warn"}>
                    {indexer.api_key.has_value ? "key set" : "no key"}
                  </Badge>
                  <label className="flex items-center gap-1 text-ink-muted">
                    <input
                      type="checkbox"
                      checked={indexer.enabled}
                      disabled={setEnabled.isPending}
                      onChange={(event) => setEnabled.mutate({ indexer, enabled: event.target.checked })}
                    />
                    enabled
                  </label>
                  <button
                    type="button"
                    aria-label={`Remove ${indexer.name}`}
                    onClick={() => remove.mutate(indexer.id)}
                    disabled={remove.isPending}
                    className="rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-50"
                  >
                    <Trash2 aria-hidden className="size-3.5" />
                  </button>
                </span>
              </div>
              <span className="text-xs text-ink-muted">
                priority {indexer.priority}
                {indexer.caps_fetched_at ? ` · capabilities last read ${new Date(indexer.caps_fetched_at).toLocaleString()}` : " · capabilities never read"}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        !indexers.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No indexer is configured.
          </p>
        )
      )}

      {(setEnabled.isError || remove.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddIndexer onAdded={invalidate} />
    </div>
  );
}

function AddIndexer({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("");
  const [impl, setImpl] = useState("newznab");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [priority, setPriority] = useState(50);

  const create = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/indexers", {
        body: { name, impl, base_url: baseUrl, api_key: apiKey || undefined, priority, enabled: true },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setName("");
      setBaseUrl("");
      setApiKey("");
      setPriority(50);
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
        <select className={inputStyle} value={impl} onChange={(event) => setImpl(event.target.value)}>
          <option value="newznab">newznab</option>
          <option value="torznab">torznab</option>
        </select>
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          placeholder="NZBgeek"
          autoComplete="off"
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Base URL
        <input
          className={inputStyle}
          value={baseUrl}
          placeholder="https://api.example.com"
          autoComplete="off"
          onChange={(event) => setBaseUrl(event.target.value)}
        />
      </label>
      <label className="flex min-w-40 flex-col gap-1 text-xs text-ink-muted">
        API key (optional)
        <input
          className={inputStyle}
          type="password"
          value={apiKey}
          autoComplete="off"
          onChange={(event) => setApiKey(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Priority
        <input
          className={`${inputStyle} w-20`}
          type="number"
          value={priority}
          onChange={(event) => setPriority(Number(event.target.value))}
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

function Badge({ tone, children }: { tone: "ok" | "warn"; children: React.ReactNode }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        tone === "ok" ? "border-border-subtle text-ink-muted" : "border-warn text-warn"
      }`}
    >
      {children}
    </span>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
