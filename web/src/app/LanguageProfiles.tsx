import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type LanguageProfile = components["schemas"]["LanguageProfile"];

function splitCodes(raw: string): string[] {
  return raw
    .split(",")
    .map((code) => code.trim())
    .filter((code) => code.length > 0);
}

/** Section 19.8's policy CRUD: internal/db/settings.Cache already validates
 *  and stores it -- this is the REST API and builder UI
 *  LibraryConfigPlaceholder's own "language" section had been standing in
 *  for (todo 4.3, 2026-09-12). Each language list is a plain comma-separated
 *  field of codes (BCP-47 or whatever the probe reports, e.g. "en, de") --
 *  no fixed enum exists to pick from. Global, not per-library, same as
 *  QualityProfiles' own doc comment explains. */
export function LanguageProfiles() {
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);

  const profiles = useQuery({
    queryKey: ["languageProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/languageprofiles");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["languageProfiles"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/languageprofiles/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">Language profiles</h1>
      <p className="text-sm leading-relaxed text-ink-muted">
        A required list means at least one listed
        language must be present, evaluated from the probe, never the release name. An empty
        profile carries no language policy at all.
      </p>
      {remove.isError && (
        <p className="text-xs text-danger">
          {(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}
        </p>
      )}

      {profiles.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {profiles.error !== null && (
        <p className="text-sm text-danger">
          {(profiles.error as { detail?: string }).detail ?? "Language profiles could not be read."}
        </p>
      )}

      {profiles.data && profiles.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {profiles.data.map((profile) =>
            editingId === profile.id ? (
              <li key={profile.id} className="p-3">
                <LanguageProfileForm
                  initial={profile}
                  onDone={() => {
                    setEditingId(null);
                    invalidate();
                  }}
                  onCancel={() => setEditingId(null)}
                />
              </li>
            ) : (
              <li key={profile.id} className="flex flex-wrap items-center gap-2 px-3 py-2.5">
                <span className="text-sm font-medium">{profile.name}</span>
                {profile.audio_required.length > 0 && (
                  <span className="text-xs text-ink-muted">
                    requires {profile.audio_required.join(", ")}
                  </span>
                )}
                {profile.reject_dubbed && (
                  <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
                    rejects dubbed
                  </span>
                )}
                <button
                  type="button"
                  onClick={() => setEditingId(profile.id)}
                  className="ml-auto rounded-md border border-border-subtle px-2 py-1 text-xs
                             text-ink-muted hover:text-ink focus-visible:outline-2
                             focus-visible:outline-offset-2 focus-visible:outline-accent"
                >
                  Edit
                </button>
                <button
                  type="button"
                  aria-label={`Remove language profile ${profile.name}`}
                  disabled={remove.isPending}
                  onClick={() => remove.mutate(profile.id)}
                  className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                             disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                             focus-visible:outline-accent"
                >
                  <Trash2 aria-hidden className="size-3.5" />
                </button>
              </li>
            ),
          )}
        </ul>
      ) : (
        !profiles.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
            No language profile exists yet.
          </p>
        )
      )}

      {creating ? (
        <div className="rounded-lg border border-border-subtle bg-surface p-3">
          <LanguageProfileForm
            onDone={() => {
              setCreating(false);
              invalidate();
            }}
            onCancel={() => setCreating(false)}
          />
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setCreating(true)}
          className="w-fit rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          New language profile
        </button>
      )}
    </div>
  );
}

function LanguageProfileForm({
  initial,
  onDone,
  onCancel,
}: {
  initial?: LanguageProfile;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [audioPreferred, setAudioPreferred] = useState((initial?.audio_preferred ?? []).join(", "));
  const [audioRequired, setAudioRequired] = useState((initial?.audio_required ?? []).join(", "));
  const [subtitlePreferred, setSubtitlePreferred] = useState(
    (initial?.subtitle_preferred ?? []).join(", "),
  );
  const [subtitleRequired, setSubtitleRequired] = useState(
    (initial?.subtitle_required ?? []).join(", "),
  );
  const [preferOriginalAudio, setPreferOriginalAudio] = useState(initial?.prefer_original_audio ?? false);
  const [rejectDubbed, setRejectDubbed] = useState(initial?.reject_dubbed ?? false);

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name,
        audio_preferred: splitCodes(audioPreferred),
        audio_required: splitCodes(audioRequired),
        subtitle_preferred: splitCodes(subtitlePreferred),
        subtitle_required: splitCodes(subtitleRequired),
        prefer_original_audio: preferOriginalAudio,
        reject_dubbed: rejectDubbed,
      };
      const { error } = initial
        ? await api.PATCH("/languageprofiles/{id}", { params: { path: { id: initial.id } }, body })
        : await api.POST("/languageprofiles", { body });
      if (error) throw error;
    },
    onSuccess: onDone,
  });

  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          autoComplete="off"
          autoFocus
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <div className="grid grid-cols-2 gap-2">
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Audio preferred
          <input
            className={inputStyle}
            value={audioPreferred}
            placeholder="en, de"
            autoComplete="off"
            onChange={(event) => setAudioPreferred(event.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Audio required (any_of)
          <input
            className={inputStyle}
            value={audioRequired}
            placeholder="en"
            autoComplete="off"
            onChange={(event) => setAudioRequired(event.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Subtitle preferred
          <input
            className={inputStyle}
            value={subtitlePreferred}
            autoComplete="off"
            onChange={(event) => setSubtitlePreferred(event.target.value)}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Subtitle required (any_of)
          <input
            className={inputStyle}
            value={subtitleRequired}
            autoComplete="off"
            onChange={(event) => setSubtitleRequired(event.target.value)}
          />
        </label>
      </div>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        <input
          type="checkbox"
          checked={preferOriginalAudio}
          onChange={(event) => setPreferOriginalAudio(event.target.checked)}
          className="size-3.5"
        />
        Prefer original audio
      </label>
      <label className="flex items-center gap-2 text-xs text-ink-muted">
        <input
          type="checkbox"
          checked={rejectDubbed}
          onChange={(event) => setRejectDubbed(event.target.checked)}
          className="size-3.5"
        />
        Reject dubbed
      </label>
      {save.isError && (
        <p className="text-xs text-danger">
          {(save.error as { detail?: string }).detail ?? "That profile could not be saved."}
        </p>
      )}
      <div className="flex gap-2">
        <button
          type="submit"
          disabled={!name.trim() || save.isPending}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          Cancel
        </button>
      </div>
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
