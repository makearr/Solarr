import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Info } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type MetadataSource = components["schemas"]["MetadataSource"];
type Settings = components["schemas"]["MetadataSettings"];
type CoverSource = NonNullable<Settings["cover_sources"]>[number];

const COVER_SOURCES: readonly SourceEntry<CoverSource>[] = [
  {
    id: "coverartarchive",
    label: "Cover Art Archive",
    note: "MusicBrainz's cover archive, fetched by release and then by release group.",
  },
  {
    id: "embedded",
    label: "Embedded art",
    note: "The picture already inside your files, used where nothing earlier found a cover.",
  },
  {
    id: "theaudiodb",
    label: "TheAudioDB",
    note: "Album covers from theaudiodb.com, by release group, where nothing earlier found one.",
  },
];

/** What each source is, and what using it means. The BrainzMash note is a
 *  privacy disclosure: it is a third party, and searches from this library
 *  reach it. */
const SOURCES: readonly SourceEntry<MetadataSource>[] = [
  {
    id: "brainzmash",
    label: "BrainzMash",
    note:
      "A third-party service, not run by this project and not by MusicBrainz. Artist and album " +
      "searches from your library are sent to it, and its operator can see them. It is fast and " +
      "answers more than direct MusicBrainz, which is why it is tried first by default.",
  },
  {
    id: "musicbrainz-proxy",
    label: "Lidarr metadata server",
    note: "The Servarr project's own MusicBrainz proxy. Searches reach it rather than MusicBrainz directly.",
  },
  {
    id: "musicbrainz",
    label: "MusicBrainz",
    note:
      "The upstream database the others proxy. Rate-limited to one request a second, so it is " +
      "slower, but it always answers. It cannot be removed from the order, only moved.",
  },
];

/** The metadata providers' own settings. Advanced tier, like Network: a working library needs none of it changed. */
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

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  // brainzmash_user_agent is required on every PATCH, so each change carries the stored one.
  const patch = useMutation({
    mutationFn: async (change: Partial<Settings>) => {
      const { error } = await api.PATCH("/metadata/settings", {
        body: { brainzmash_user_agent: metadata.data?.brainzmash_user_agent ?? "", ...change },
      });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["metadata-settings"] }),
  });

  const saveTheAudioDBKey = useMutation({
    mutationFn: async (key: string) => {
      const { error } = await api.POST("/metadata/theaudiodb/apikey", { body: { api_key: key } });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["metadata-settings"] }),
  });

  const saveAcoustIDKey = useMutation({
    mutationFn: async (key: string) => {
      const { error } = await api.POST("/metadata/acoustid/apikey", { body: { api_key: key } });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["metadata-settings"] }),
  });

  if (!isTierVisible(tier, "advanced")) return null;
  if (metadata.error !== null || !metadata.data) return null;

  const settings = metadata.data;
  const musicLibraries = (categories.data ?? [])
    .filter((category) => category.media_type === "music")
    .map((category) => category.name);

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Metadata</h2>
      <p className="text-xs text-ink-muted">
        Sources are set per media type; every library of that type uses them.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <div className="flex flex-col gap-0.5">
          <h3 className="text-sm font-medium text-ink">Music</h3>
          <p className="text-xs text-ink-muted">
            {musicLibraries.length === 0
              ? "No music library uses these yet."
              : `Used by ${musicLibraries.join(", ")}.`}
          </p>
        </div>

        <p className="text-xs text-ink-muted">
          Artist and album data is asked from each source in turn until one answers.
        </p>
        <OrderedSources
          title="Source order"
          sources={SOURCES}
          order={(settings.source_order ?? []) as MetadataSource[]}
          required="musicbrainz"
          requiredNote="MusicBrainz always answers as a last resort"
          busy={patch.isPending}
          onChange={(order) => patch.mutate({ source_order: order })}
        />
        <UserAgentForm
          key={settings.brainzmash_user_agent}
          stored={settings.brainzmash_user_agent}
          onSave={(agent) => patch.mutate({ brainzmash_user_agent: agent })}
          busy={patch.isPending}
        />

        <div className="flex flex-col gap-3 border-t border-border-subtle pt-3">
          <p className="text-xs text-ink-muted">
            Covers come from each source in turn; a later one only fills what the earlier ones left
            missing.
          </p>
          <OrderedSources
            title="Cover sources"
            sources={COVER_SOURCES}
            order={settings.cover_sources ?? []}
            busy={patch.isPending}
            onChange={(order) => patch.mutate({ cover_sources: order })}
          />
          <Switch
            label="Wikidata"
            note="Artist photos and biographies."
            checked={settings.wikidata ?? true}
            busy={patch.isPending}
            onChange={(wikidata) => patch.mutate({ wikidata })}
          />
          <Switch
            label="TheAudioDB"
            note="Artist photos and biographies where Wikidata has none."
            checked={settings.theaudiodb ?? true}
            busy={patch.isPending}
            onChange={(theaudiodb) => patch.mutate({ theaudiodb })}
          />
          <BackfillForm
            key={settings.people_backfill_per_run ?? 200}
            stored={settings.people_backfill_per_run ?? 200}
            onSave={(perRun) => patch.mutate({ people_backfill_per_run: perRun })}
            busy={patch.isPending}
          />
          <Switch
            label="AcoustID fingerprints"
            note="Identifies a file by its sound when its tags and folder name cannot. Needs the key below."
            checked={settings.acoustid ?? true}
            busy={patch.isPending}
            onChange={(acoustid) => patch.mutate({ acoustid })}
          />
        </div>
        {patch.isError && <p className="text-xs text-danger">That change was refused.</p>}

        <ApiKeyForm
          key={`acoustid-${String(settings.acoustid_api_key?.has_value ?? false)}`}
          label="AcoustID API key"
          unset="not set"
          stored={settings.acoustid_api_key?.has_value ?? false}
          onSave={(apiKey) => saveAcoustIDKey.mutate(apiKey)}
          busy={saveAcoustIDKey.isPending}
        >
          Identifies a file by its sound when nothing else can name it. Without a key that step is
          skipped and everything else still works. Free, and it takes a minute: register an
          application at{" "}
          <a
            href="https://acoustid.org/new-application"
            target="_blank"
            rel="noreferrer"
            className="text-accent underline underline-offset-2"
          >
            acoustid.org/new-application
          </a>
          . The key it gives you is the one to paste here -- the key on your own account page is a
          different one, for submitting fingerprints, and lookups refuse it.
        </ApiKeyForm>
        {saveAcoustIDKey.isError && (
          <p className="text-xs text-danger">That key could not be stored.</p>
        )}
        <ApiKeyForm
          key={`theaudiodb-${String(settings.theaudiodb_api_key?.has_value ?? false)}`}
          label="TheAudioDB API key"
          unset="the free public key is used"
          stored={settings.theaudiodb_api_key?.has_value ?? false}
          onSave={(apiKey) => saveTheAudioDBKey.mutate(apiKey)}
          busy={saveTheAudioDBKey.isPending}
        >
          Optional. TheAudioDB works without one; a supporter key from theaudiodb.com replaces the
          public key and its limits.
        </ApiKeyForm>
        {saveTheAudioDBKey.isError && (
          <p className="text-xs text-danger">That key could not be stored.</p>
        )}
      </div>
    </section>
  );
}

function Switch({
  label,
  note,
  checked,
  busy,
  onChange,
}: {
  label: string;
  note: string;
  checked: boolean;
  busy: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex items-start gap-2 text-xs">
      <input
        type="checkbox"
        checked={checked}
        disabled={busy}
        className="mt-0.5"
        onChange={(event) => onChange(event.target.checked)}
      />
      <span className="flex flex-col">
        <span className="text-ink">{label}</span>
        <span className="text-[0.6875rem] text-ink-muted">{note}</span>
      </span>
    </label>
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
  children,
}: {
  label: string;
  unset: string;
  stored: boolean;
  onSave: (apiKey: string) => void;
  busy: boolean;
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
          maxLength={128}
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

type SourceEntry<T extends string> = { id: T; label: string; note: string };

/** The order sources are tried in, Lidarr's indexer-priority shape. Each row
 *  carries an info button whose tooltip says what the source is -- for
 *  BrainzMash that is a privacy disclosure. */
function OrderedSources<T extends string>({
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
