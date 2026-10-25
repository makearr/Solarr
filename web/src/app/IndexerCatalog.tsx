import { useState } from "react";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import {
  NO_FILTER, alreadyAdded, filterCatalog, indexerSecret, missingDownloader, missingIndexerFields, type CatalogFilter,
} from "@/lib/indexerCatalog";
import { useDownloaderKinds, useIndexerKinds } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { inputStyle } from "@/ui/form";

type IndexerKind = components["schemas"]["IndexerKind"];

const PROTOCOLS = { usenet: "Usenet", torrent: "Torrent", ddl: "Soulseek" } as const;
const PRIVACY = { public: "Public", "semi-private": "Semi-private", private: "Private" } as const;
const MEDIA = { music: "Music", tv: "TV", movies: "Movies", anime: "Anime", books: "Books" } as const;

const useIndexers = () => useQuery({ queryKey: ["indexers"], queryFn: () => ok(api.GET("/indexers")) });
const useDownloaders = () => useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });

/** Prowlarr's add list: every indexer Solarr can search, narrowed by words, protocol, privacy and media. Every entry
 *  is optional; the chosen ones are set up on the next page. */
export function IndexerCatalog() {
  const kinds = useIndexerKinds();
  const indexers = useIndexers();
  const navigate = useNavigate();
  const [filter, setFilter] = useState<CatalogFilter>(NO_FILTER);
  const [chosen, setChosen] = useState<string[]>([]);
  const shown = filterCatalog(kinds.data ?? [], filter);
  const toggle = (id: string, on: boolean) => setChosen((current) => (on ? [...current, id] : current.filter((one) => one !== id)));

  return (
    <div className="mx-auto flex h-full max-w-4xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <Link to="/connections/indexers" className="text-xs text-accent underline underline-offset-2">← Indexers</Link>
        <h1 className="text-lg font-semibold">Add indexers</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Tick the ones to add; the next page asks for their addresses and keys. None is added until you confirm.
        </p>
      </header>

      <div className="flex flex-wrap items-end gap-2 text-xs">
        <label className="flex min-w-56 flex-1 flex-col gap-1 text-ink-muted">
          Search
          <input
            className={inputStyle}
            value={filter.text}
            placeholder="Name or description"
            onChange={(event) => setFilter({ ...filter, text: event.target.value })}
          />
        </label>
        <FilterSelect label="Protocol" value={filter.protocol} options={PROTOCOLS} onChange={(protocol) => setFilter({ ...filter, protocol })} />
        <FilterSelect label="Privacy" value={filter.privacy} options={PRIVACY} onChange={(privacy) => setFilter({ ...filter, privacy })} />
        <FilterSelect label="Media" value={filter.media} options={MEDIA} onChange={(media) => setFilter({ ...filter, media })} />
      </div>

      {kinds.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {kinds.isError && <p className="text-sm text-danger">The indexer list could not be read.</p>}
      {kinds.data && (
        <p className="text-xs text-ink-muted">
          {shown.length} of {kinds.data.length} indexers
        </p>
      )}
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {shown.map((kind) => {
          const added = alreadyAdded(kind, indexers.data ?? []);
          return (
            <li key={kind.id}>
              <label className={`flex items-start gap-3 p-3 ${added ? "opacity-60" : "cursor-pointer hover:bg-canvas"}`}>
                <input
                  type="checkbox"
                  className="mt-1"
                  aria-label={`Add ${kind.name}`}
                  checked={chosen.includes(kind.id)}
                  onChange={(event) => toggle(kind.id, event.target.checked)}
                />
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="flex flex-wrap items-center gap-2">
                    <span className="font-medium">{kind.name}</span>
                    <Chip>{PROTOCOLS[kind.protocol]}</Chip>
                    <Chip tone={kind.privacy === "public" ? "ok" : undefined}>{PRIVACY[kind.privacy]}</Chip>
                    <span className="text-xs text-ink-muted">{kind.media_types.map((media) => MEDIA[media]).join(", ")}</span>
                    <span className="text-xs text-ink-muted">{kind.language}</span>
                    {added && <span className="text-xs text-ink-muted">already added</span>}
                  </span>
                  <span className="text-xs text-ink-muted">{kind.description}</span>
                </span>
              </label>
            </li>
          );
        })}
      </ul>

      <div className="sticky bottom-0 flex items-center gap-3 border-t border-border-subtle bg-canvas py-3">
        <span className="text-sm text-ink-muted">{chosen.length} chosen</span>
        <Button
          variant="primary"
          disabled={chosen.length === 0}
          onClick={() => void navigate({ to: "/connections/indexers/add/setup", search: { ids: chosen.join(",") } })}
        >
          Next
        </Button>
        {chosen.length > 0 && (
          <button type="button" onClick={() => setChosen([])} className="text-xs text-ink-muted hover:text-ink">
            Clear
          </button>
        )}
      </div>
    </div>
  );
}

function FilterSelect<T extends string>({
  label, value, options, onChange,
}: { label: string; value: "" | T; options: Record<T, string>; onChange: (value: "" | T) => void }) {
  return (
    <label className="flex flex-col gap-1 text-ink-muted">
      {label}
      <select className={inputStyle} value={value} onChange={(event) => onChange(event.target.value as "" | T)}>
        <option value="">Any</option>
        {(Object.keys(options) as T[]).map((key) => (
          <option key={key} value={key}>{options[key]}</option>
        ))}
      </select>
    </label>
  );
}

function Chip({ tone, children }: { tone?: "ok"; children: React.ReactNode }) {
  return (
    <span className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${tone === "ok" ? "border-ok text-ok" : "border-border-subtle text-ink-muted"}`}>
      {children}
    </span>
  );
}

/** What one chosen entry is set up with on the second page. */
interface Draft {
  address: string;
  user: string;
  secret: string;
  companion: boolean;
}

type Outcome = { state: "added" } | { state: "failed"; message: string };

/** The second page: each chosen entry's address and credentials, a test for each, then all are added at once. */
export function IndexerSetup() {
  const search = useSearch({ strict: false }) as { ids?: string };
  const ids = (search.ids ?? "").split(",").filter(Boolean);
  const kinds = useIndexerKinds();
  const clientKinds = useDownloaderKinds();
  const downloaders = useDownloaders();
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const chosen = ids.map((id) => kinds.data?.find((kind) => kind.id === id)).filter((kind): kind is IndexerKind => kind !== undefined);
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  const draftOf = (kind: IndexerKind): Draft => drafts[kind.id] ?? { address: kind.base_urls[0] ?? "", user: "", secret: "", companion: false };
  const setDraft = (kind: IndexerKind, change: Partial<Draft>) => setDrafts((current) => ({ ...current, [kind.id]: { ...draftOf(kind), ...change } }));
  const [outcomes, setOutcomes] = useState<Record<string, Outcome>>({});

  const ready = chosen.length > 0 && chosen.every((kind) => {
    const draft = draftOf(kind);
    return missingIndexerFields(kind, draft.address, draft.user, draft.secret).length === 0;
  });

  const addAll = useMutation({
    mutationFn: async () => {
      const results: Record<string, Outcome> = {};
      for (const kind of chosen) {
        if (outcomes[kind.id]?.state === "added") {
          results[kind.id] = { state: "added" };
          continue;
        }
        const draft = draftOf(kind);
        const { data, error } = await api.POST("/indexers", {
          body: {
            name: kind.name, impl: kind.impl, definition: kind.id, base_url: draft.address.trim(), priority: 25, enabled: true,
            api_key: indexerSecret(kind, draft.user, draft.secret) || undefined,
          },
        });
        if (error || !data) {
          results[kind.id] = { state: "failed", message: (error as { detail?: string } | undefined)?.detail ?? "It could not be added." };
          continue;
        }
        if (draft.companion) {
          const companion = await api.POST("/indexers/{id}/companion", { params: { path: { id: data.id } } });
          if (companion.error) {
            results[kind.id] = { state: "failed", message: `Added; its download client was not: ${(companion.error as { detail?: string }).detail ?? ""}` };
            continue;
          }
        }
        results[kind.id] = { state: "added" };
      }
      return results;
    },
    onSuccess: (results) => {
      setOutcomes(results);
      void queryClient.invalidateQueries({ queryKey: ["indexers"] });
      void queryClient.invalidateQueries({ queryKey: ["downloaders"] });
      if (Object.values(results).every((result) => result.state === "added")) void navigate({ to: "/connections/indexers" });
    },
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <Link to="/connections/indexers/add" className="text-xs text-accent underline underline-offset-2">← Choose others</Link>
        <h1 className="text-lg font-semibold">Set up {chosen.length} indexer{chosen.length === 1 ? "" : "s"}</h1>
        <p className="text-sm leading-relaxed text-ink-muted">Each needs its address and, for a private one, its key. Test any before adding.</p>
      </header>
      {kinds.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {!kinds.isLoading && chosen.length === 0 && (
        <p className="text-sm text-ink-muted">
          Nothing is chosen. <Link to="/connections/indexers/add" className="text-accent underline">Choose indexers</Link>.
        </p>
      )}
      {chosen.map((kind) => (
        <SetupCard
          key={kind.id}
          kind={kind}
          draft={draftOf(kind)}
          onChange={(change) => setDraft(kind, change)}
          outcome={outcomes[kind.id]}
          companion={missingDownloader(kind, draftOf(kind).address, downloaders.data ?? [], clientKinds.data ?? [])?.label}
        />
      ))}
      {chosen.length > 0 && (
        <div className="flex items-center gap-3">
          <Button variant="primary" disabled={!ready || addAll.isPending} onClick={() => addAll.mutate()}>
            {addAll.isPending ? "Adding…" : `Add ${chosen.length} indexer${chosen.length === 1 ? "" : "s"}`}
          </Button>
          {!ready && <span className="text-xs text-ink-muted">Fill in what each one still needs.</span>}
        </div>
      )}
    </div>
  );
}

function SetupCard({
  kind, draft, onChange, outcome, companion,
}: { kind: IndexerKind; draft: Draft; onChange: (change: Partial<Draft>) => void; outcome?: Outcome; companion?: string }) {
  const test = useMutation({
    mutationFn: () => ok(api.POST("/indexers/test", {
      body: { impl: kind.impl, base_url: draft.address.trim(), api_key: indexerSecret(kind, draft.user, draft.secret) || undefined },
    })),
  });
  const missing = missingIndexerFields(kind, draft.address, draft.user, draft.secret);
  const presets = kind.base_urls;
  const custom = presets.length === 0 || !presets.includes(draft.address);
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="font-medium">{kind.name}</h2>
        <Chip>{PROTOCOLS[kind.protocol]}</Chip>
        <Chip tone={kind.privacy === "public" ? "ok" : undefined}>{PRIVACY[kind.privacy]}</Chip>
        {outcome?.state === "added" && <span className="text-xs text-ok">added</span>}
      </div>
      <div className="flex flex-wrap items-end gap-2 text-xs">
        {presets.length > 1 && (
          <label className="flex flex-col gap-1 text-ink-muted">
            Mirror
            <select
              className={inputStyle}
              value={custom ? "" : draft.address}
              onChange={(event) => onChange({ address: event.target.value })}
            >
              {presets.map((url) => <option key={url} value={url}>{url}</option>)}
              <option value="">Other…</option>
            </select>
          </label>
        )}
        {(presets.length <= 1 || custom) && (
          <label className="flex min-w-56 flex-1 flex-col gap-1 text-ink-muted">
            Address
            <input
              className={inputStyle}
              value={draft.address}
              placeholder={presets[0] ?? "https://indexer.example/"}
              autoComplete="off"
              onChange={(event) => onChange({ address: event.target.value })}
            />
          </label>
        )}
        {kind.user_label && (
          <label className="flex min-w-36 flex-col gap-1 text-ink-muted">
            {kind.user_label}
            <input className={inputStyle} value={draft.user} autoComplete="off" onChange={(event) => onChange({ user: event.target.value })} />
          </label>
        )}
        {kind.secret_label && (
          <label className="flex min-w-48 flex-col gap-1 text-ink-muted">
            {kind.secret_label}
            {kind.secret_optional ? " (optional)" : ""}
            <input
              className={inputStyle}
              type="password"
              value={draft.secret}
              autoComplete="off"
              onChange={(event) => onChange({ secret: event.target.value })}
            />
          </label>
        )}
        <button
          type="button"
          disabled={missing.length > 0 || test.isPending}
          onClick={() => test.mutate()}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-ink-muted hover:text-ink disabled:opacity-50"
        >
          {test.isPending ? "Testing…" : "Test"}
        </button>
      </div>
      {companion && (
        <label className="flex items-center gap-2 text-xs text-ink-muted">
          <input type="checkbox" checked={draft.companion} onChange={(event) => onChange({ companion: event.target.checked })} />
          Also add {companion} as a download client at this address, with the same key (its releases download through it)
        </label>
      )}
      {missing.length > 0 && <p className="text-xs text-ink-muted">Still needed: {missing.join(", ")}.</p>}
      {test.data && <p className={`text-xs ${test.data.ok ? "text-ok" : "text-danger"}`}>{test.data.message}</p>}
      {test.isError && <p className="text-xs text-danger">The test could not run.</p>}
      {outcome?.state === "failed" && <p className="text-xs text-danger">{outcome.message}</p>}
    </section>
  );
}
