import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Info } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type MetadataSource = components["schemas"]["MetadataSource"];

/** What each source is, and what using it means. The BrainzMash note is a
 *  privacy disclosure: it is a third party, and searches from this library
 *  reach it. */
const SOURCES: readonly { id: MetadataSource; label: string; note: string }[] = [
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

  const update = useMutation({
    mutationFn: async (brainzmashUserAgent: string) => {
      const { error } = await api.PATCH("/metadata/settings", {
        body: { brainzmash_user_agent: brainzmashUserAgent },
      });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["metadata-settings"] }),
  });

  const reorder = useMutation({
    mutationFn: async (order: MetadataSource[]) => {
      const { error } = await api.PATCH("/metadata/settings", {
        body: {
          brainzmash_user_agent: metadata.data?.brainzmash_user_agent ?? "",
          source_order: order,
        },
      });
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

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Metadata</h2>
      <p className="text-xs text-ink-muted">
        Artist and album data is asked from each source in turn until one answers.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <SourceOrder
          order={(metadata.data.source_order ?? []) as MetadataSource[]}
          busy={reorder.isPending}
          onChange={(order) => reorder.mutate(order)}
        />
        {reorder.isError && <p className="text-xs text-danger">That order was refused.</p>}
        <UserAgentForm
          key={metadata.data.brainzmash_user_agent}
          stored={metadata.data.brainzmash_user_agent}
          onSave={(agent) => update.mutate(agent)}
          busy={update.isPending}
        />
        {update.isError && <p className="text-xs text-danger">That change was refused.</p>}
        <AcoustIDKeyForm
          key={String(metadata.data.acoustid_api_key?.has_value ?? false)}
          stored={metadata.data.acoustid_api_key?.has_value ?? false}
          onSave={(apiKey) => saveAcoustIDKey.mutate(apiKey)}
          busy={saveAcoustIDKey.isPending}
        />
        {saveAcoustIDKey.isError && (
          <p className="text-xs text-danger">That key could not be stored.</p>
        )}
      </div>
    </section>
  );
}

/** The AcoustID application key. A stored key is never shown, not even masked,
 *  so the field stands empty and says whether one is set -- typing replaces it,
 *  Clear removes it. */
function AcoustIDKeyForm({
  stored,
  onSave,
  busy,
}: {
  stored: boolean;
  onSave: (apiKey: string) => void;
  busy: boolean;
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
          AcoustID API key
          <span className={stored ? "text-ok" : "text-ink-muted"}>
            {stored ? "stored" : "not set"}
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
        <span className="text-[0.6875rem]">
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
        </span>
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

/** The order sources are tried in, Lidarr's indexer-priority shape. Each row
 *  carries an info button whose tooltip says what the source is -- for
 *  BrainzMash that is a privacy disclosure. */
function SourceOrder({
  order,
  busy,
  onChange,
}: {
  order: MetadataSource[];
  busy: boolean;
  onChange: (order: MetadataSource[]) => void;
}) {
  const listed = order.filter((id) => SOURCES.some((source) => source.id === id));
  const absent = SOURCES.filter((source) => !listed.includes(source.id));

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
      <span className="text-ink-muted">Source order</span>
      {listed.map((id, index) => {
        const source = SOURCES.find((entry) => entry.id === id);
        if (source === undefined) return null;
        // MusicBrainz always answers as a last resort, so it can be moved but
        // never taken out of the list.
        const removable = source.id !== "musicbrainz";
        return (
          <div key={id} className="flex items-center gap-1">
            <input
              type="checkbox"
              checked
              disabled={busy || !removable}
              aria-label={`Use ${source.label}`}
              title={removable ? undefined : "MusicBrainz always answers as a last resort"}
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
function SourceInfo({ source }: { source: (typeof SOURCES)[number] }) {
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

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
