import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleHelp } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { missingDownloader } from "@/lib/indexerCatalog";
import { useDownloaderKinds, useIndexerKinds } from "@/lib/resources";
import { usePreference } from "@/lib/usePreference";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Button, RemoveButton } from "@/ui/Button";
import { inputStyle, labelStyle, addFormStyle } from "@/ui/form";

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
          Where releases are searched for. Choose them from the list of every indexer Solarr can search, or keep them in step
          with a Prowlarr under Connections → Arr.
        </p>
        <Link
          to="/connections/indexers/add"
          className="mt-1 w-fit rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink"
        >
          Add indexers…
        </Link>
      </header>

      <Proxies />

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
                  {indexer.source && <Badge tone="ok">{indexer.source.awaiting_key || !indexer.source.upstream_base_url ? "via Prowlarr" : "from Prowlarr"}</Badge>}
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
                    {indexer.enabled ? "active" : "paused"}
                  </label>
                  <button
                    type="button"
                    onClick={() => test.mutate(indexer.id)}
                    disabled={test.isPending}
                    className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink disabled:opacity-50"
                  >
                    {test.isPending && test.variables === indexer.id ? "Testing…" : "Test"}
                  </button>
                  <RemoveButton
                    aria-label={`Remove ${indexer.name}`}
                    onClick={() => remove.mutate(indexer.id)}
                    disabled={remove.isPending}
                  />
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
              <UpstreamKey indexer={indexer} onSaved={invalidate} />
              <ProxyPicker indexer={indexer} onSaved={invalidate} />
              <ClientPicker indexer={indexer} onSaved={invalidate} />
              <QueryLimit indexer={indexer} onSaved={invalidate} />
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
      className={addFormStyle}
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
      {create.isError && <p className="w-full text-xs text-danger">{(create.error as { detail?: string } | null)?.detail ?? "That connection could not be created."}</p>}
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

/** "3000, 3040" as numbers; anything that is not a number is dropped. */
function parseCategories(text: string): number[] {
  return text
    .split(/[\s,]+/)
    .map((part) => Number(part))
    .filter((category) => Number.isInteger(category) && category > 0);
}

/** D17: the Newznab categories music is searched and RSS-read in, editable in place. */
function MusicCategories({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const current = (indexer.music_categories ?? []).join(", ");
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
          placeholder="from the indexer"
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
      <span>{listedMedia(indexer.categories_json)}</span>
    </form>
  );
}

const mediaRoots: [string, number][] = [["movies", 2000], ["music", 3000], ["TV", 5000], ["adult", 6000], ["books", 7000]];

/** Which media types the indexer's own capabilities list a category for; asked only for those. */
function listedMedia(capsJson: string | undefined): string {
  if (!capsJson) return "capabilities not read yet: Test reads them";
  try {
    const caps = JSON.parse(capsJson) as { Categories?: { ID: number; Subcategories?: { ID: number }[] }[] };
    const ids = (caps.Categories ?? []).flatMap((category) => [category.ID, ...(category.Subcategories ?? []).map((sub) => sub.ID)]);
    if (ids.length === 0) return "lists no categories: asked for everything";
    const found = mediaRoots.filter(([, root]) => ids.some((id) => id >= root && id < root + 1000)).map(([name]) => name);
    return found.length > 0 ? `lists ${found.join(", ")}` : "lists no media type Solarr serves";
  } catch {
    return "";
  }
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

type ProxyEntry = components["schemas"]["Proxy"];

/** The list of proxies indexers can go through: HTTP, SOCKS5 and FlareSolverr, or those a synced Prowlarr brought. */
function Proxies() {
  const queryClient = useQueryClient();
  const proxies = useQuery({ queryKey: ["proxies"], queryFn: () => ok(api.GET("/proxies")) });
  const [name, setName] = useState("");
  const [kind, setKind] = useState<ProxyEntry["kind"]>("flaresolverr");
  const [address, setAddress] = useState("");
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["proxies"] });
    void queryClient.invalidateQueries({ queryKey: ["indexers"] });
  };
  const add = useMutation({
    mutationFn: () => ok(api.POST("/proxies", { body: { name, kind, url: address } })),
    onSuccess: () => {
      setName("");
      setAddress("");
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: (id: number) => ok(api.DELETE("/proxies/{id}", { params: { path: { id } } })),
    onSuccess: refresh,
  });
  return (
    <details className="rounded-lg border border-border-subtle bg-surface p-3 text-sm">
      <summary className="cursor-pointer font-medium">Proxies ({proxies.data?.length ?? 0})</summary>
      <ul className="mt-2 flex flex-col gap-1 text-xs">
        {proxies.data?.map((proxy) => (
          <li key={proxy.id} className="flex items-center gap-2">
            <span className="text-ink">{proxy.name}</span>
            <Badge tone="ok">{proxy.kind}</Badge>
            <span className="truncate font-mono text-ink-muted">{proxy.url}</span>
            {proxy.source_arr_id && <span className="text-ink-muted">· from Prowlarr</span>}
            <span className="ml-auto">
              <RemoveButton aria-label={`Remove ${proxy.name}`} onClick={() => remove.mutate(proxy.id)} disabled={remove.isPending} />
            </span>
          </li>
        ))}
        {proxies.data?.length === 0 && <li className="text-ink-muted">None yet. Prowlarr's can be synced from its connection.</li>}
      </ul>
      {remove.isError && <p className="mt-1 text-xs text-danger">{(remove.error as { detail?: string }).detail ?? "It could not be removed."}</p>}
      <form
        className="mt-2 flex flex-wrap items-center gap-2 text-xs"
        onSubmit={(event) => {
          event.preventDefault();
          add.mutate();
        }}
      >
        <input className={`${inputStyle} w-36`} placeholder="Name" value={name} onChange={(event) => setName(event.target.value)} />
        <select className={inputStyle} value={kind} onChange={(event) => setKind(event.target.value as ProxyEntry["kind"])}>
          <option value="flaresolverr">FlareSolverr</option>
          <option value="http">HTTP</option>
          <option value="socks5">SOCKS5</option>
        </select>
        <input
          className={`${inputStyle} w-64`}
          placeholder={kind === "socks5" ? "socks5://host:1080" : kind === "http" ? "http://host:3128" : "http://host:8191"}
          value={address}
          onChange={(event) => setAddress(event.target.value)}
        />
        <button type="submit" disabled={!name || !address || add.isPending} className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink disabled:opacity-50">
          Add proxy
        </button>
        {add.isError && <span className="text-danger">{(add.error as { detail?: string }).detail ?? "It could not be added."}</span>}
      </form>
    </details>
  );
}

/** Which proxy of the list one indexer's requests go through. */
/** An indexer taken over from Prowlarr that still searches through it, because Prowlarr hides the site's key. */
function UpstreamKey({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const [key, setKey] = useState("");
  const use = useMutation({
    mutationFn: () => ok(api.POST("/indexers/{id}/use-upstream", { params: { path: { id: indexer.id } }, body: { api_key: key } })),
    onSuccess: () => {
      setKey("");
      onSaved();
    },
  });
  if (!indexer.source?.awaiting_key || !indexer.source.upstream_base_url) return null;
  return (
    <span className="flex flex-wrap items-center gap-2 rounded-md border border-border-subtle p-2 text-xs text-ink-muted">
      Searches through Prowlarr, which hides the key of {indexer.source.upstream_base_url}. Enter the key to search it directly.
      <input
        type="password"
        autoComplete="off"
        aria-label={`API key of ${indexer.name}`}
        value={key}
        onChange={(event) => setKey(event.target.value)}
        className={inputStyle}
      />
      <button
        type="button"
        disabled={use.isPending}
        onClick={() => use.mutate()}
        className="rounded-md border border-border-subtle px-2 py-0.5 text-ink hover:border-accent disabled:opacity-50"
      >
        Use it directly
      </button>
      {use.isError && <span className="text-danger">{(use.error as { detail?: string }).detail ?? "It could not be changed."}</span>}
    </span>
  );
}

function ProxyPicker({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const proxies = useQuery({ queryKey: ["proxies"], queryFn: () => ok(api.GET("/proxies")) });
  const save = useMutation({
    mutationFn: (proxyId: number) =>
      ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: indexer.id } },
        body: {
          name: indexer.name, impl: indexer.impl, base_url: indexer.base_url, priority: indexer.priority,
          enabled: indexer.enabled, egress_proxy_url: indexer.egress_proxy_url, music_categories: indexer.music_categories,
          proxy_id: proxyId,
        },
      })),
    onSuccess: onSaved,
  });
  if (!proxies.data || proxies.data.length === 0) return null;
  return (
    <label className="flex items-center gap-1.5 text-xs text-ink-muted">
      Proxy
      <select
        className={inputStyle}
        value={indexer.proxy_id ?? 0}
        disabled={save.isPending}
        onChange={(event) => save.mutate(Number(event.target.value))}
      >
        <option value={0}>None</option>
        {proxies.data.map((proxy) => (
          <option key={proxy.id} value={proxy.id}>{proxy.name} ({proxy.kind})</option>
        ))}
      </select>
    </label>
  );
}

/** Which download client an indexer's releases go to: any that takes the protocol, or one of them. */
function ClientPicker({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const downloaders = useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });
  const save = useMutation({
    mutationFn: (clientId: number) =>
      ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: indexer.id } },
        body: {
          name: indexer.name, impl: indexer.impl, base_url: indexer.base_url, priority: indexer.priority,
          enabled: indexer.enabled, egress_proxy_url: indexer.egress_proxy_url, music_categories: indexer.music_categories,
          download_client_id: clientId,
        },
      })),
    onSuccess: onSaved,
  });
  const protocol = kindProtocol(indexer.impl);
  const fitting = (downloaders.data ?? []).filter((client) => client.protocol === protocol);
  if (fitting.length < 2 && !indexer.download_client_id) return null;
  return (
    <label className="flex items-center gap-1.5 text-xs text-ink-muted">
      Download client
      <select
        className={inputStyle}
        value={indexer.download_client_id ?? 0}
        disabled={save.isPending}
        onChange={(event) => save.mutate(Number(event.target.value))}
      >
        <option value={0}>Any that takes {protocol}</option>
        {fitting.map((client) => (
          <option key={client.id} value={client.id}>{client.name}</option>
        ))}
      </select>
    </label>
  );
}

/** How an implementation's releases are downloaded: usenet for Newznab, soulseek's own, else torrent. */
function kindProtocol(impl: string): string {
  if (impl === "newznab") return "usenet";
  if (impl === "soulseek") return "ddl";
  return "torrent";
}

/** The most requests an hour the indexer allows, if it states one; Solarr paces itself 20% under it and backs off on a limit error. */
function QueryLimit({ indexer, onSaved }: { indexer: Indexer; onSaved: () => void }) {
  const [text, setText] = useState(indexer.queries_per_hour ? String(indexer.queries_per_hour) : "");
  const save = useMutation({
    mutationFn: (limit: number) =>
      ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: indexer.id } },
        body: {
          name: indexer.name, impl: indexer.impl, base_url: indexer.base_url, priority: indexer.priority,
          enabled: indexer.enabled, egress_proxy_url: indexer.egress_proxy_url, music_categories: indexer.music_categories,
          queries_per_hour: limit,
        },
      })),
    onSuccess: onSaved,
  });
  const current = indexer.queries_per_hour ? String(indexer.queries_per_hour) : "";
  return (
    <form
      className="flex items-center gap-2 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(Math.max(0, Math.round(Number(text) || 0)));
      }}
    >
      <label className="flex items-center gap-1.5" title="Requests an hour the indexer allows. Empty: none stated, so Solarr asks gently and slows down when the indexer says it is overloaded.">
        Requests per hour
        <input className={`${inputStyle} w-20`} inputMode="numeric" value={text} placeholder="no limit" onChange={(event) => setText(event.target.value)} />
      </label>
      {text !== current && (
        <button type="submit" disabled={save.isPending} className="rounded-md border border-border-subtle px-2 py-0.5 hover:text-ink">
          Save
        </button>
      )}
    </form>
  );
}
