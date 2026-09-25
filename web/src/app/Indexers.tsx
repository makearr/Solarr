import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleHelp, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type Indexer = components["schemas"]["Indexer"];
type ConnectionTestResult = components["schemas"]["ConnectionTestResult"];

/** Stage 5, ADR-37/43/47: Newznab/Torznab search providers. A public
 *  Torznab endpoint may need no API key at all -- left blank, the
 *  connection is created with none rather than refused. */
export function Indexers() {
  const queryClient = useQueryClient();
  const expert = isTierVisible(useTier(), "expert");
  const [explainPriority, setExplainPriority] = useState(false);

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
      // PATCH replaces every field this form can change -- egress_proxy_url
      // has to be resent from the current row or toggling "enabled" would
      // silently wipe it (the same staleness shape todo 4.6's category edit
      // already found once, avoided here by construction).
      const { error } = await api.PATCH("/indexers/{id}", {
        params: { path: { id: args.indexer.id } },
        body: {
          name: args.indexer.name, impl: args.indexer.impl, base_url: args.indexer.base_url,
          priority: args.indexer.priority, enabled: args.enabled,
          egress_proxy_url: args.indexer.egress_proxy_url,
        },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const [testResults, setTestResults] = useState<Record<number, ConnectionTestResult>>({});
  const test = useMutation({
    mutationFn: async (id: number) => {
      const { data, error } = await api.POST("/indexers/{id}/test", { params: { path: { id } } });
      if (error) throw error;
      return { id, result: data! };
    },
    onSuccess: ({ id, result }) => setTestResults((prev) => ({ ...prev, [id]: result })),
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
          Where releases are searched for. Add them from Prowlarr, or one at a time below.
        </p>
      </header>

      <AddFromProwlarr onAdded={invalidate} />

      <div className="flex items-center gap-2">
        <h2 className="text-sm font-medium">Connected, by priority</h2>
        <button
          type="button"
          aria-label="What priority means"
          aria-expanded={explainPriority}
          onClick={() => setExplainPriority(!explainPriority)}
          className="rounded-full text-ink-muted hover:text-ink"
        >
          <CircleHelp aria-hidden className="size-4" />
        </button>
      </div>
      {explainPriority && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs leading-relaxed text-ink-muted">
          Priority runs from 1 to 50, and lower is preferred, as in Lidarr. Every enabled indexer is
          searched; when two of them list the same release, the one with the lower number is the one
          it is downloaded from. Indexers with equal priority are treated alike. New indexers start at 25.
        </p>
      )}

      {indexers.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {indexers.error !== null && <p className="text-sm text-danger">Indexers could not be read.</p>}

      {indexers.data && indexers.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {[...indexers.data].sort((a, b) => a.priority - b.priority || a.name.localeCompare(b.name)).map((indexer) => (
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
                    onClick={() => test.mutate(indexer.id)}
                    disabled={test.isPending}
                    className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink disabled:opacity-50"
                  >
                    {test.isPending && test.variables === indexer.id ? "Testing…" : "Test"}
                  </button>
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
                {expert && indexer.egress_proxy_url ? ` · FlareSolverr proxy ${indexer.egress_proxy_url}` : ""}
              </span>
              {(() => {
                const result = testResults[indexer.id];
                return result && (
                  <span className={`text-xs ${result.ok ? "text-ok" : "text-danger"}`}>{result.message}</span>
                );
              })()}
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

      <details className="rounded-lg border border-border-subtle bg-surface p-3">
        <summary className="cursor-pointer text-sm">Add one indexer by hand</summary>
        <div className="mt-3">
          <AddIndexer onAdded={invalidate} expert={expert} />
        </div>
      </details>
    </div>
  );
}

function AddIndexer({ onAdded, expert }: { onAdded: () => void; expert: boolean }) {
  const [name, setName] = useState("");
  const [impl, setImpl] = useState("newznab");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [priority, setPriority] = useState(25);
  const [egressProxyUrl, setEgressProxyUrl] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/indexers", {
        body: {
          name, impl, base_url: baseUrl, api_key: apiKey || undefined, priority, enabled: true,
          egress_proxy_url: egressProxyUrl || undefined,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setName("");
      setBaseUrl("");
      setApiKey("");
      setPriority(25);
      setEgressProxyUrl("");
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
          min={1}
          max={50}
          value={priority}
          onChange={(event) => setPriority(Number(event.target.value))}
        />
      </label>
      {expert && (
        <label className="flex min-w-48 flex-col gap-1 text-xs text-ink-muted">
          FlareSolverr proxy (optional)
          <input
            className={inputStyle}
            value={egressProxyUrl}
            placeholder="http://flaresolverr:8191"
            autoComplete="off"
            onChange={(event) => setEgressProxyUrl(event.target.value)}
          />
        </label>
      )}
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

type ProwlarrIndexer = components["schemas"]["ProwlarrIndexer"];

/** Lidarr's Prowlarr sync, from this side: point at Prowlarr once, add what it serves. */
function AddFromProwlarr({ onAdded }: { onAdded: () => void }) {
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [chosen, setChosen] = useState<Set<number>>(new Set());

  const load = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/indexers/prowlarr", { body: { base_url: baseUrl, api_key: apiKey } });
      if (error) throw error;
      return data;
    },
    // Offered by default: usenet indexers that can find music and are enabled in Prowlarr.
    // Torrents wait for a torrent client, which Solarr does not have yet.
    onSuccess: (found) =>
      setChosen(
        new Set(
          found
            .filter((entry) => entry.music && entry.enabled && !entry.already_added && entry.impl === "newznab")
            .map((entry) => entry.prowlarr_id),
        ),
      ),
  });
  const add = useMutation({
    mutationFn: async (entries: ProwlarrIndexer[]) => {
      for (const entry of entries) {
        const { error } = await api.POST("/indexers", {
          body: { name: entry.name, impl: entry.impl, base_url: entry.base_url, api_key: apiKey, enabled: true, priority: 25 },
        });
        if (error) throw error;
      }
    },
    onSuccess: () => {
      onAdded();
      load.mutate();
    },
  });

  const found = load.data ?? [];
  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
      <h2 className="text-sm font-medium">Prowlarr</h2>
      <form
        className="flex flex-wrap items-end gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          load.mutate();
        }}
      >
        <label className="flex min-w-56 flex-1 flex-col gap-1 text-xs text-ink-muted">
          Address
          <input className={inputStyle} value={baseUrl} placeholder="http://prowlarr:9696" onChange={(event) => setBaseUrl(event.target.value)} />
        </label>
        <label className="flex min-w-48 flex-col gap-1 text-xs text-ink-muted">
          API key
          <input className={inputStyle} type="password" autoComplete="off" value={apiKey} onChange={(event) => setApiKey(event.target.value)} />
        </label>
        <button
          type="submit"
          disabled={!baseUrl.trim() || !apiKey.trim() || load.isPending}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink disabled:opacity-50"
        >
          {load.isPending ? "Reading…" : "Show its indexers"}
        </button>
      </form>
      {load.isError && (
        <p className="text-xs text-danger">{(load.error as { detail?: string }).detail ?? "Prowlarr could not be read."}</p>
      )}
      {found.length > 0 && (
        <>
          <ul className="flex flex-col gap-1 text-sm">
            {found.map((entry) => (
              <li key={entry.prowlarr_id} className="flex items-center gap-2">
                <input
                  type="checkbox"
                  aria-label={`Add ${entry.name}`}
                  disabled={entry.already_added}
                  checked={entry.already_added || chosen.has(entry.prowlarr_id)}
                  onChange={(event) => {
                    const next = new Set(chosen);
                    if (event.target.checked) next.add(entry.prowlarr_id);
                    else next.delete(entry.prowlarr_id);
                    setChosen(next);
                  }}
                />
                <span>{entry.name}</span>
                {entry.impl === "torznab" ? <Badge tone="warn">torrent: no torrent client yet</Badge> : <Badge tone="ok">usenet</Badge>}
                {!entry.music && <Badge tone="warn">no music categories</Badge>}
                {!entry.enabled && <Badge tone="warn">disabled in Prowlarr</Badge>}
                {entry.already_added && <span className="text-xs text-ink-muted">already added</span>}
              </li>
            ))}
          </ul>
          <button
            type="button"
            disabled={chosen.size === 0 || add.isPending}
            onClick={() => add.mutate(found.filter((entry) => chosen.has(entry.prowlarr_id)))}
            className="w-fit rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
          >
            {add.isPending ? "Adding…" : `Add ${chosen.size} indexer${chosen.size === 1 ? "" : "s"}`}
          </button>
          {add.isError && <p className="text-xs text-danger">Not every indexer could be added.</p>}
        </>
      )}
    </section>
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
