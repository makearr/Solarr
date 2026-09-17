import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";

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

  if (!isTierVisible(tier, "advanced")) return null;
  if (metadata.error !== null || !metadata.data) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Metadata</h2>
      <p className="text-xs text-ink-muted">
        Artist and album data is asked from BrainzMash first, then the Lidarr metadata server, then
        MusicBrainz.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <UserAgentForm
          key={metadata.data.brainzmash_user_agent}
          stored={metadata.data.brainzmash_user_agent}
          onSave={(agent) => update.mutate(agent)}
          busy={update.isPending}
        />
        {update.isError && <p className="text-xs text-danger">That change was refused.</p>}
      </div>
    </section>
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
