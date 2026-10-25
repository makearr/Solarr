import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleHelp, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { missingDownloader } from "@/lib/indexerCatalog";
import { useDownloaderKinds, useIndexerKinds } from "@/lib/resources";
import { usePreference } from "@/lib/usePreference";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

type Indexer = components["schemas"]["Indexer"];
type ConnectionTestResult = components["schemas"]["ConnectionTestResult"];

/** Stage 5, ADR-37/43/47: search providers -- Newznab/Torznab, Prowlarr's API indexers and Soulseek, chosen from
 *  the catalog (/connections/indexers/add). A public one may need no API key at all. */
export function Indexers() {
  const queryClient = useQueryClient();
  const kinds = useIndexerKinds();
  const clientKinds = useDownloaderKinds();
  const downloaders = useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });
  const [dismissed, setDismissed] = usePreference<string[]>("companion.dismissed", []);
  const kindOf = (indexer: Indexer) =>
    kinds.data?.find((kind) => kind.id === indexer.definition) ?? kinds.data?.find((kind) => kind.impl === indexer.impl);
  const addCompanion = useMutation({
    mutationFn: (id: number) => ok(api.POST("/indexers/{id}/companion", { params: { path: { id } } })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["downloaders"] }),
  });
  const expert = isTierVisible(useTier(), "expert");
  const [explainPriority, setExplainPriority] = useState(false);

  const indexers = useQuery({
    queryKey: ["indexers"],
    queryFn: () => ok(api.GET("/indexers")),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["indexers"] });

  const update = useMutation({
    mutationFn: async (args: { indexer: Indexer; enabled: boolean; priority: number }) => {
      // PATCH replaces every field this form can change -- egress_proxy_url
      // has to be resent from the current row or toggling "enabled" would
      // silently wipe it (the same staleness shape todo 4.6's category edit
      // already found once, avoided here by construction).
      await ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: args.indexer.id } },
        body: {
          name: args.indexer.name, impl: args.indexer.impl, base_url: args.indexer.base_url,
          priority: args.priority, enabled: args.enabled,
          egress_proxy_url: args.indexer.egress_proxy_url,
          music_categories: args.indexer.music_categories,
        },
      }));
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
      await ok(api.DELETE("/indexers/{id}", { params: { path: { id } } }));
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Indexers</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Where releases are searched for. Choose them from the list of every indexer Solarr can search, add them from
          Prowlarr, or add one by hand below.
        </p>
        <Link
          to="/connections/indexers/add"
          className="mt-1 w-fit rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink"
        >
          Add indexers…
        </Link>
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
          searched. When two releases are otherwise equal -- the same quality, score and match -- the one
          from the indexer with the lower number is grabbed, and when two indexers list the same release
          it is downloaded from that one. Indexers with equal priority are treated alike. New indexers start at 25.
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
                  {kindOf(indexer)?.name ?? indexer.impl}
                </span>
                <span className="truncate font-mono text-xs text-ink-muted">{indexer.base_url}</span>
                <span className="ml-auto flex shrink-0 items-center gap-2 text-xs">
                  {/* A public indexer that takes no key has nothing to warn about. */}
                  {!kindOf(indexer) || kindOf(indexer)?.secret_label !== undefined ? (
                    <Badge tone={indexer.api_key.has_value || kindOf(indexer)?.secret_optional ? "ok" : "warn"}>
                      {indexer.api_key.has_value ? "key set" : "no key"}
                    </Badge>
                  ) : null}
                  <label className="flex items-center gap-1 text-ink-muted">
                    <input
                      type="checkbox"
                      checked={indexer.enabled}
                      disabled={update.isPending}
                      onChange={(event) => update.mutate({ indexer, enabled: event.target.checked, priority: indexer.priority })}
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
              <span className="flex flex-wrap items-center gap-1 text-xs text-ink-muted">
                <PriorityInput
                  key={indexer.priority}
                  value={indexer.priority}
                  busy={update.isPending}
                  onSave={(priority) => update.mutate({ indexer, enabled: indexer.enabled, priority })}
                />
                {indexer.caps_fetched_at ? ` · capabilities last read ${new Date(indexer.caps_fetched_at).toLocaleString()}` : " · capabilities never read"}
                {expert && indexer.egress_proxy_url ? ` · FlareSolverr proxy ${indexer.egress_proxy_url}` : ""}
              </span>
              <MusicCategories indexer={indexer} onSaved={invalidate} />
              {(() => {
                const companion = missingDownloader(kindOf(indexer), indexer.base_url, downloaders.data ?? [], clientKinds.data ?? []);
                const key = `indexer:${indexer.id}`;
                return companion && downloaders.data && !dismissed.includes(key) && (
                  <span className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle p-2 text-xs text-ink-muted">
                    Its releases download through {companion.label}, which is not a download client yet.
                    <button
                      type="button"
                      disabled={addCompanion.isPending}
                      onClick={() => addCompanion.mutate(indexer.id)}
                      className="rounded-md border border-border-subtle px-2 py-0.5 text-ink hover:border-accent disabled:opacity-50"
                    >
                      Add {companion.label} as a download client
                    </button>
                    <button type="button" onClick={() => setDismissed([...dismissed, key])} className="hover:text-ink">
                      Not now
                    </button>
                    {addCompanion.isError && <span className="text-danger">It could not be added.</span>}
                  </span>
                );
              })()}
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

      {(update.isError || remove.isError) && (
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
  const [categories, setCategories] = useState("3000");

  const create = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/indexers", {
        body: {
          name, impl, base_url: baseUrl, api_key: apiKey || undefined, priority, enabled: true,
          egress_proxy_url: egressProxyUrl || undefined,
          music_categories: parseCategories(categories),
        },
      }));
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
      <label className={labelStyle}>
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
      <label className={labelStyle}>
        Music categories
        <input
          className={`${inputStyle} w-28`}
          value={categories}
          title="Newznab category numbers, comma separated: 3000 is Audio, 3040 Audio/Lossless"
          onChange={(event) => setCategories(event.target.value)}
        />
      </label>
      <label className={labelStyle}>
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
      <Button
        variant="primary"
        type="submit"
        disabled={!name.trim() || !baseUrl.trim() || create.isPending}
      >
        {create.isPending ? "Connecting…" : "Connect"}
      </Button>
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
    mutationFn: () => ok(api.POST("/indexers/prowlarr", { body: { base_url: baseUrl, api_key: apiKey } })),
    // Offered by default: usenet indexers that can find music and are enabled in Prowlarr; torrents are a tick away.
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
        await ok(api.POST("/indexers", {
          body: { name: entry.name, impl: entry.impl, base_url: entry.base_url, api_key: apiKey, enabled: true, priority: 25 },
        }));
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
                <Badge tone="ok">{entry.impl === "torznab" ? "torrent" : "usenet"}</Badge>
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

/** "3000, 3040" as numbers; anything that is not a number is dropped. */
function parseCategories(text: string): number[] {
  return text
    .split(/[\s,]+/)
    .map((part) => Number(part))
    .filter((category) => Number.isInteger(category) && category > 0);
}

/** D17: the Newznab categories music is searched and RSS-read in, editable in place. */
function MusicCategories({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const current = (indexer.music_categories ?? [3000]).join(", ");
  const [text, setText] = useState(current);
  const save = useMutation({
    mutationFn: async () => {
      await ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: indexer.id } },
        body: {
          name: indexer.name, impl: indexer.impl, base_url: indexer.base_url, priority: indexer.priority,
          enabled: indexer.enabled, egress_proxy_url: indexer.egress_proxy_url,
          music_categories: parseCategories(text),
        },
      }));
    },
    onSuccess: onSaved,
  });
  return (
    <form
      className="flex items-center gap-2 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <label className="flex items-center gap-1.5">
        Music categories
        <input
          className={`${inputStyle} w-28`}
          value={text}
          title="Newznab category numbers, comma separated: 3000 is Audio, 3040 Audio/Lossless"
          onChange={(event) => setText(event.target.value)}
        />
      </label>
      {text !== current && (
        <button type="submit" disabled={save.isPending} className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink">
          Save
        </button>
      )}
      {save.isError && <span className="text-danger">Refused.</span>}
    </form>
  );
}

/** An indexer's priority, saved when it is left or Enter is pressed; 1 to 50, lower preferred. */
function PriorityInput({ value, busy, onSave }: { value: number; busy: boolean; onSave: (priority: number) => void }) {
  const [draft, setDraft] = useState(String(value));
  const commit = () => {
    const parsed = Math.round(Number(draft));
    if (Number.isFinite(parsed) && parsed >= 1 && parsed <= 50 && parsed !== value) onSave(parsed);
    else setDraft(String(value));
  };
  return (
    <label className="flex items-center gap-1">
      priority
      <input
        type="number"
        min={1}
        max={50}
        value={draft}
        disabled={busy}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => {
          if (event.key === "Enter") event.currentTarget.blur();
        }}
        className="w-12 rounded-md border border-border-subtle bg-canvas px-1 py-0.5 text-xs text-ink
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      />
    </label>
  );
}
