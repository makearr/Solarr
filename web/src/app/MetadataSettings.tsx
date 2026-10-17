import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Info } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";
import { Chip } from "@/ui/Chip";
import { Card, Toggle } from "./SettingsCard";

type Settings = components["schemas"]["MetadataSettings"];
type Provider = components["schemas"]["MetadataProviderInfo"];

const LIBRARY_LABEL: Record<string, string> = { music: "Music", tv: "Series", movie: "Movies" };

/** What a provider is beyond what it carries. BrainzMash's is a privacy disclosure: a third party sees the searches. */
const NOTES: Record<string, string> = {
  wikidata:
    "Comes first in every library by default: the ids its item records (TheTVDB, TMDB, IMDb, TVmaze, AniList) steer the other " +
    "providers to the right entry. It also finds franchises and spin-offs for collections, matches people across libraries " +
    "and gives artists photos and biographies. No key.",
  brainzmash:
    "A third-party service, not run by this project and not by MusicBrainz. Artist and album searches from your library are " +
    "sent to it, and its operator can see them. It is fast and answers more than direct MusicBrainz.",
  "musicbrainz-proxy": "The Servarr project's own MusicBrainz proxy. Searches reach it rather than MusicBrainz directly.",
  musicbrainz: "The upstream database the others proxy: one request a second, but it always answers, so it stays on.",
  skyhook: "Sonarr's own service: series, seasons, episodes and their numbering, as Sonarr has them. No key.",
  tmdb: "Cast, crew, guest stars, artwork, certifications, studios and collections, for series and films. Free, needs your own key.",
  tvdb: "Episodes in TheTVDB's other orderings (DVD, absolute) and its artwork. Works with Solarr's own key.",
  tvmaze: "Air times with their time zone, networks and cast. No key.",
  anilist: "Anime: titles in every language, cours, sequels and spin-offs. No key.",
  radarr: "Radarr's own service: TMDB's films with IMDb, Rotten Tomatoes and Metacritic ratings and release dates. No key.",
  theaudiodb: "Artist photos and biographies where Wikidata has none, and album covers. Works with the free public key.",
  coverartarchive: "MusicBrainz's cover archive, by release and then by release group.",
  acoustid: "Identifies a file by its sound when its tags and folder name cannot. Needs a free key.",
};

/** One overview of every metadata provider: what it serves, whether it is on, and its key. The order a library asks them
 *  in is the library's own, under its Settings, Metadata. */
export function MetadataSettings() {
  const tier = useTier();
  const queryClient = useQueryClient();
  const metadata = useQuery({
    queryKey: ["metadata-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/metadata/settings");
      if (error) throw error;
      return data;
    },
  });
  const providers = useQuery({
    queryKey: ["metadata-providers"],
    queryFn: async () => {
      const { data, error } = await api.GET("/metadata/providers");
      if (error) throw error;
      return data.providers;
    },
  });
  const patch = usePatchMetadataSettings(metadata.data);
  const switchProvider = useMutation({
    mutationFn: async ({ key, enabled }: { key: string; enabled: boolean }) => {
      const { error } = await api.PUT("/metadata/providers/{key}", { params: { path: { key } }, body: { enabled } });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["metadata-providers"] });
      void queryClient.invalidateQueries({ queryKey: ["metadata-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["libraryMetadataSources"] });
    },
  });
  if (metadata.error !== null || !metadata.data || !providers.data) return null;
  const settings = metadata.data;
  return (
    <section className="flex flex-col gap-4">
      <p className="max-w-2xl text-sm text-ink-muted">
        Every provider Solarr can ask. One switched off is asked by no library; its key stays stored. The order a library asks them in is
        set under the library itself, Settings, Metadata.
      </p>
      <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {providers.data.map((provider) => (
          <ProviderRow
            key={provider.key}
            provider={provider}
            settings={settings}
            busy={switchProvider.isPending && switchProvider.variables?.key === provider.key}
            onSwitch={(enabled) => switchProvider.mutate({ key: provider.key, enabled })}
            onPatch={(change) => patch.mutate(change)}
            patching={patch.isPending}
          />
        ))}
      </ul>
      {(switchProvider.isError || patch.isError) && <p className="text-sm text-danger">That change was refused.</p>}
      {isTierVisible(tier, "advanced") && settings.availability_grace_hours && (
        <Card
          title="Air times"
          help="How long after an episode airs it is waited for before it counts as missing and is searched for: releases need a while to appear."
        >
          <GraceForm
            key={JSON.stringify(settings.availability_grace_hours)}
            stored={settings.availability_grace_hours}
            busy={patch.isPending}
            onSave={(grace) => patch.mutate({ availability_grace_hours: grace })}
          />
        </Card>
      )}
    </section>
  );
}

/** Changes the metadata settings; the BrainzMash User-Agent is required on every change, so each carries the stored one. */
export function usePatchMetadataSettings(stored: Settings | undefined) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (change: Partial<Settings>) => {
      const { error } = await api.PATCH("/metadata/settings", { body: { brainzmash_user_agent: stored?.brainzmash_user_agent ?? "", ...change } });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["metadata-settings"] }),
  });
}

function useKeySaver(path: "/metadata/tmdb/apikey" | "/metadata/tvdb/apikey" | "/metadata/theaudiodb/apikey" | "/metadata/acoustid/apikey") {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (key: string) => {
      const { error } = await api.POST(path, { body: { api_key: key } });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["metadata-settings"] });
      void queryClient.invalidateQueries({ queryKey: ["tv-providers"] });
    },
  });
}

/** Whether a provider has what it needs, in a word: its key, or none needed. */
function keyState(key: string, settings: Settings): { label: string; tone?: "warn" } | undefined {
  switch (key) {
    case "tmdb":
      return settings.tmdb_api_key?.has_value ? { label: "your key" } : { label: "needs your key", tone: "warn" };
    case "tvdb":
      return { label: settings.tvdb_api_key?.has_value ? "your key" : "built-in key" };
    case "theaudiodb":
      return { label: settings.theaudiodb_api_key?.has_value ? "your key" : "public key" };
    case "acoustid":
      return settings.acoustid_api_key?.has_value ? { label: "your key" } : { label: "needs your key", tone: "warn" };
  }
  return undefined;
}

function ProviderRow({
  provider,
  settings,
  busy,
  onSwitch,
  onPatch,
  patching,
}: {
  provider: Provider;
  settings: Settings;
  busy: boolean;
  onSwitch: (enabled: boolean) => void;
  onPatch: (change: Partial<Settings>) => void;
  patching: boolean;
}) {
  const [open, setOpen] = useState(false);
  const libraries = [...new Set([...provider.media_types, ...provider.also_for])];
  const state = keyState(provider.key, settings);
  return (
    <li className="flex flex-col gap-2 p-3">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="text-left text-sm font-medium hover:underline focus-visible:outline-2 focus-visible:outline-accent"
        >
          {provider.label}
        </button>
        {libraries.map((library) => (
          <Chip key={library}>{LIBRARY_LABEL[library] ?? library}</Chip>
        ))}
        {state && <Chip tone={state.tone}>{state.label}</Chip>}
        <span className="min-w-0 flex-1 truncate text-xs text-ink-muted" title={provider.carries.join(", ")}>
          {provider.carries.join(", ")}
        </span>
        {provider.can_disable ? (
          <Toggle label={provider.enabled ? "On" : "Off"} checked={provider.enabled} disabled={busy} onChange={onSwitch} />
        ) : (
          <span className="text-xs text-ink-muted">always on</span>
        )}
      </div>
      {open && (
        <div className="flex flex-col gap-3 pl-1">
          {NOTES[provider.key] && <p className="max-w-2xl text-xs text-ink-muted">{NOTES[provider.key]}</p>}
          <ProviderDetails providerKey={provider.key} settings={settings} onPatch={onPatch} patching={patching} />
        </div>
      )}
    </li>
  );
}

/** What a provider asks of its owner: a key, a User-Agent, how much it fills per run. */
function ProviderDetails({
  providerKey,
  settings,
  onPatch,
  patching,
}: {
  providerKey: string;
  settings: Settings;
  onPatch: (change: Partial<Settings>) => void;
  patching: boolean;
}) {
  const tmdb = useKeySaver("/metadata/tmdb/apikey");
  const tvdb = useKeySaver("/metadata/tvdb/apikey");
  const audiodb = useKeySaver("/metadata/theaudiodb/apikey");
  const acoustid = useKeySaver("/metadata/acoustid/apikey");
  const failed = <p className="text-xs text-danger">That key could not be stored.</p>;
  switch (providerKey) {
    case "brainzmash":
      return <UserAgentForm key={settings.brainzmash_user_agent} stored={settings.brainzmash_user_agent} onSave={(agent) => onPatch({ brainzmash_user_agent: agent })} busy={patching} />;
    case "wikidata":
      return (
        <BackfillForm
          key={settings.people_backfill_per_run ?? 200}
          stored={settings.people_backfill_per_run ?? 200}
          onSave={(perRun) => onPatch({ people_backfill_per_run: perRun })}
          busy={patching}
        />
      );
    case "tmdb":
      return (
        <>
          <ApiKeyForm key={`tmdb-${String(settings.tmdb_api_key?.has_value ?? false)}`} label="TMDB key" unset="not set" stored={settings.tmdb_api_key?.has_value ?? false} onSave={(key) => tmdb.mutate(key)} busy={tmdb.isPending} maxLength={512}>
            Free: create an account at{" "}
            <a href="https://www.themoviedb.org/settings/api" target="_blank" rel="noreferrer" className="text-accent underline underline-offset-2">
              themoviedb.org/settings/api
            </a>{" "}
            and paste either the API Read Access Token (the long one) or the API key.
          </ApiKeyForm>
          {tmdb.isError && failed}
          <ProviderTest />
        </>
      );
    case "tvdb":
      return (
        <>
          <ApiKeyForm key={`tvdb-${String(settings.tvdb_api_key?.has_value ?? false)}`} label="TheTVDB key" unset="the built-in key is used" stored={settings.tvdb_api_key?.has_value ?? false} onSave={(key) => tvdb.mutate(key)} busy={tvdb.isPending}>
            Optional. TheTVDB works with Solarr&apos;s own project key; a key of your own from thetvdb.com replaces it.
          </ApiKeyForm>
          {tvdb.isError && failed}
        </>
      );
    case "theaudiodb":
      return (
        <>
          <ApiKeyForm key={`theaudiodb-${String(settings.theaudiodb_api_key?.has_value ?? false)}`} label="TheAudioDB API key" unset="the free public key is used" stored={settings.theaudiodb_api_key?.has_value ?? false} onSave={(key) => audiodb.mutate(key)} busy={audiodb.isPending}>
            Optional. A supporter key from theaudiodb.com replaces the public key and its limits.
          </ApiKeyForm>
          {audiodb.isError && failed}
        </>
      );
    case "acoustid":
      return (
        <>
          <ApiKeyForm key={`acoustid-${String(settings.acoustid_api_key?.has_value ?? false)}`} label="AcoustID API key" unset="not set" stored={settings.acoustid_api_key?.has_value ?? false} onSave={(key) => acoustid.mutate(key)} busy={acoustid.isPending}>
            Without a key that step is skipped and everything else still works. Register an application at{" "}
            <a href="https://acoustid.org/new-application" target="_blank" rel="noreferrer" className="text-accent underline underline-offset-2">
              acoustid.org/new-application
            </a>
            ; the key it gives you is the one to paste here, not the one on your account page.
          </ApiKeyForm>
          {acoustid.isError && failed}
        </>
      );
    case "skyhook":
    case "tvmaze":
    case "anilist":
      return <ProviderTest />;
  }
  return null;
}

/** Asks every series provider once and says which answered. */
function ProviderTest() {
  const providers = useQuery({
    queryKey: ["tv-providers"],
    queryFn: async () => {
      const { data, error } = await api.GET("/metadata/tv/providers");
      if (error) throw error;
      return data;
    },
  });
  const test = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/metadata/tv/providers/test");
      if (error) throw error;
      return data;
    },
  });
  const results = new Map((test.data ?? []).map((result) => [result.id, result]));
  return (
    <div className="flex flex-col gap-1 text-xs">
      <button
        type="button"
        disabled={test.isPending}
        onClick={() => test.mutate()}
        className="w-fit rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {test.isPending ? "Testing..." : "Test the series providers"}
      </button>
      {test.isError && <span className="text-danger">The test could not run.</span>}
      {(providers.data ?? []).map((provider) => {
        const result = results.get(provider.id);
        return result ? (
          <span key={provider.id} className="flex gap-2">
            <span className="text-ink">{provider.name}</span>
            <span className={result.ok ? "text-ok" : "text-danger"}>{result.message}</span>
          </span>
        ) : null;
      })}
    </div>
  );
}

/** A provider's API key. A stored key is never shown, not even masked, so the field stands empty
 *  and says whether one is set -- typing replaces it, Clear removes it. */
function ApiKeyForm({
  label,
  unset,
  stored,
  onSave,
  busy,
  maxLength = 128,
  children,
}: {
  label: string;
  unset: string;
  stored: boolean;
  onSave: (apiKey: string) => void;
  busy: boolean;
  maxLength?: number;
  children: React.ReactNode;
}) {
  const [apiKey, setApiKey] = useState("");

  return (
    <form
      className="flex flex-col gap-3 border-t border-border-subtle pt-3 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        onSave(apiKey.trim());
        setApiKey("");
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="flex items-center gap-2">
          {label}
          <span className={stored ? "text-ok" : "text-ink-muted"}>
            {stored ? "stored" : unset}
          </span>
        </span>
        <input
          type="password"
          value={apiKey}
          disabled={busy}
          spellCheck={false}
          autoComplete="off"
          maxLength={maxLength}
          placeholder={stored ? "A key is stored. Type a new one to replace it." : "Paste the key"}
          onChange={(event) => setApiKey(event.target.value)}
          className={`${inputStyle} max-w-md font-mono`}
        />
        <span className="text-[0.6875rem]">{children}</span>
      </label>

      <div className="flex gap-2">
        {apiKey.trim() !== "" && (
          <button
            type="submit"
            disabled={busy}
            className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Save
          </button>
        )}
        {stored && (
          <button
            type="button"
            disabled={busy}
            onClick={() => {
              setApiKey("");
              onSave("");
            }}
            className="w-fit rounded-md border border-border-subtle px-2 py-1 text-ink-muted
                       hover:text-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Clear
          </button>
        )}
      </div>
    </form>
  );
}

export type SourceEntry<T extends string> = { id: T; label: string; note: string };

/** The order sources are tried in, Lidarr's indexer-priority shape. Each row
 *  carries an info button whose tooltip says what the source is -- for
 *  BrainzMash that is a privacy disclosure. */
export function OrderedSources<T extends string>({
  title,
  sources,
  order,
  required,
  requiredNote,
  busy,
  onChange,
}: {
  title: string;
  sources: readonly SourceEntry<T>[];
  order: T[];
  required?: T;
  requiredNote?: string;
  busy: boolean;
  onChange: (order: T[]) => void;
}) {
  const listed = order.filter((id) => sources.some((source) => source.id === id));
  const absent = sources.filter((source) => !listed.includes(source.id));

  const move = (index: number, delta: number) => {
    const target = index + delta;
    const moving = listed[index];
    const displaced = listed[target];
    if (moving === undefined || displaced === undefined) return;
    const next = [...listed];
    next[index] = displaced;
    next[target] = moving;
    onChange(next);
  };

  return (
    <div className="flex flex-col gap-1 text-xs">
      <span className="text-ink-muted">{title}</span>
      {listed.map((id, index) => {
        const source = sources.find((entry) => entry.id === id);
        if (source === undefined) return null;
        const removable = source.id !== required;
        return (
          <div key={id} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked
              disabled={busy || !removable}
              aria-label={`Use ${source.label}`}
              title={removable ? undefined : requiredNote}
              onChange={() => onChange(listed.filter((entry) => entry !== id))}
            />
            <span className="w-4 text-ink-muted">{index + 1}.</span>
            <span className="flex-1 text-ink">{source.label}</span>
            <SourceInfo source={source} />
            <button
              type="button"
              className="rounded p-0.5 text-ink-muted hover:text-ink disabled:opacity-30"
              disabled={index === 0 || busy}
              aria-label={`Move ${source.label} up`}
              onClick={() => move(index, -1)}
            >
              <ArrowUp size={12} />
            </button>
            <button
              type="button"
              className="rounded p-0.5 text-ink-muted hover:text-ink disabled:opacity-30"
              disabled={index === listed.length - 1 || busy}
              aria-label={`Move ${source.label} down`}
              onClick={() => move(index, 1)}
            >
              <ArrowDown size={12} />
            </button>
          </div>
        );
      })}

      {absent.length > 0 && (
        <div className="mt-1 flex flex-col gap-1 border-t border-border-subtle pt-2">
          {absent.map((source) => (
            <div key={source.id} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={false}
                disabled={busy}
                aria-label={`Use ${source.label}`}
                onChange={() => onChange([...listed, source.id])}
              />
              <span className="w-4" />
              <span className="flex-1 text-ink-muted">{source.label}</span>
              <SourceInfo source={source} />
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

/** A tooltip rather than standing text, on the user's own choice (2026-09-18).
 *  Hover and focus open it; there is deliberately no click toggle, because a
 *  click also focuses, so the two fought and the tooltip never appeared.
 *  title carries the same text for assistive technology and for a native
 *  hover tip. */
function SourceInfo({ source }: { source: SourceEntry<string> }) {
  const [open, setOpen] = useState(false);
  return (
    <span className="relative flex items-center">
      <button
        type="button"
        title={source.note}
        aria-label={`What is ${source.label}?`}
        aria-expanded={open}
        className="rounded p-0.5 text-ink-muted hover:text-ink"
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => setOpen(false)}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
      >
        <Info size={12} />
      </button>
      {open && (
        <span
          role="tooltip"
          className="absolute right-0 top-5 z-10 w-72 rounded-md border border-border-subtle
                     bg-surface p-2 text-[0.6875rem] leading-relaxed text-ink-muted shadow-lg"
        >
          {source.note}
        </span>
      )}
    </span>
  );
}

function UserAgentForm({
  stored,
  onSave,
  busy,
}: {
  stored: string;
  onSave: (agent: string) => void;
  busy: boolean;
}) {
  const [agent, setAgent] = useState(stored);
  const dirty = agent !== stored;

  return (
    <form
      className="flex flex-col gap-3 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        onSave(agent.trim());
      }}
    >
      <label className="flex flex-col gap-1">
        BrainzMash User-Agent
        <input
          type="text"
          value={agent}
          disabled={busy}
          spellCheck={false}
          maxLength={256}
          onChange={(event) => setAgent(event.target.value)}
          className={`${inputStyle} max-w-md font-mono`}
        />
        <span className="text-[0.6875rem]">
          BrainzMash refuses requests whose User-Agent does not start with "Lidarr/". Default
          Lidarr/2.9.6.4552. Applies to the next request.
        </span>
      </label>

      {dirty && (
        <button
          type="submit"
          disabled={busy || agent.trim() === ""}
          className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
    </form>
  );
}

/** How many artists one enrichment run gives a photo and biography. */
type Grace = { high: number; medium: number; low: number };

const GRACE_FIELDS: readonly { key: keyof Grace; label: string; help: string }[] = [
  { key: "high", label: "After an exact air time", help: "The provider states the minute it airs. Default 2 hours." },
  { key: "medium", label: "After a likely air time", help: "A provider worked the time out from the network's schedule. Default 12 hours." },
  { key: "low", label: "After an air date", help: "Only the day is known; counted from its start. Default 36 hours." },
];

function GraceForm({ stored, onSave, busy }: { stored: Grace; onSave: (grace: Grace) => void; busy: boolean }) {
  const [draft, setDraft] = useState<Record<keyof Grace, string>>({
    high: String(stored.high), medium: String(stored.medium), low: String(stored.low),
  });
  const values = { high: Number(draft.high), medium: Number(draft.medium), low: Number(draft.low) };
  const valid = Object.values(values).every((hours) => Number.isInteger(hours) && hours >= 0 && hours <= 336);
  const changed = values.high !== stored.high || values.medium !== stored.medium || values.low !== stored.low;
  return (
    <form
      className="flex flex-col gap-3 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        if (valid) onSave(values);
      }}
    >
      {GRACE_FIELDS.map((field) => (
        <label key={field.key} className="flex flex-col gap-1">
          {field.label}
          <span className="flex items-center gap-1.5">
            <input
              type="number"
              min={0}
              max={336}
              value={draft[field.key]}
              disabled={busy}
              onChange={(event) => setDraft({ ...draft, [field.key]: event.target.value })}
              className={`${inputStyle} w-20`}
            />
            hours
          </span>
          <span className="text-[0.6875rem]">{field.help}</span>
        </label>
      ))}
      {changed && (
        <button
          type="submit"
          disabled={busy || !valid}
          className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
    </form>
  );
}

function BackfillForm({
  stored,
  onSave,
  busy,
}: {
  stored: number;
  onSave: (perRun: number) => void;
  busy: boolean;
}) {
  const [perRun, setPerRun] = useState(String(stored));
  const value = Number(perRun);
  const valid = Number.isInteger(value) && value >= 1 && value <= 10000;

  return (
    <form
      className="flex flex-col gap-3 text-xs text-ink-muted"
      onSubmit={(event) => {
        event.preventDefault();
        if (valid) onSave(value);
      }}
    >
      <label className="flex flex-col gap-1">
        Artists filled per run
        <input
          type="number"
          min={1}
          max={10000}
          value={perRun}
          disabled={busy}
          onChange={(event) => setPerRun(event.target.value)}
          className={`${inputStyle} w-28`}
        />
        <span className="text-[0.6875rem]">
          How many artists each metadata refresh gives a photo and biography. Higher fills a new
          library sooner; each artist costs a few requests. Default 200, at most 10,000.
        </span>
      </label>

      {value !== stored && (
        <button
          type="submit"
          disabled={busy || !valid}
          className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
