import { useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { isTierVisible, useTier } from "@/lib/useTier";

type QualityProfile = components["schemas"]["QualityProfile"];

/** ADR-40's policy CRUD: internal/identify/score already interprets the
 *  rules/upgrade JSON, and internal/db/settings.Cache already validates and
 *  stores it -- this is the REST API and builder UI LibraryConfigPlaceholder's
 *  own "quality" section had been standing in for (todo 4.3, 2026-09-12).
 *  rules/upgrade stay raw JSON textareas rather than a visual gate/tier
 *  builder: the interpreter's own shape (hard gates, then tiers, then an
 *  additive score, ADR-40) is expressive enough that a form covering it
 *  would just be a worse JSON editor -- not attempted without a real need
 *  for one. Global, not per-library: reached from a library's config
 *  section, same as Packs, but the same list regardless of which category
 *  got you here (a category merely references one by id, not built yet --
 *  desired_item creation for music is itself blocked on this existing). */
export function QualityProfiles() {
  const tier = useTier();
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<number | null>(null);
  const [creating, setCreating] = useState(false);

  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["qualityProfiles"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/qualityprofiles/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex max-w-3xl flex-col gap-3 p-6">
      <h1 className="text-lg font-semibold">Quality</h1>
      <LibraryProfilePicker profiles={profiles.data ?? []} />
      {!isTierVisible(tier, "expert") ? null : (
        <>
          <h2 className="pt-4 text-sm font-medium">All profiles</h2>
          <p className="text-sm leading-relaxed text-ink-muted">
            Hard gates, then tiers, then an additive score (ADR-40). Editing the rules directly is
            expert work; picking one for a library above is not.
          </p>
        </>
      )}
      {isTierVisible(tier, "expert") && remove.isError && (
        <p className="text-xs text-danger">
          {(remove.error as { detail?: string }).detail ?? "That profile could not be removed."}
        </p>
      )}

      {profiles.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {profiles.error !== null && (
        <p className="text-sm text-danger">
          {(profiles.error as { detail?: string }).detail ?? "Quality profiles could not be read."}
        </p>
      )}

      {profiles.data && profiles.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {profiles.data.map((profile) =>
            editingId === profile.id ? (
              <li key={profile.id} className="p-3">
                <QualityProfileForm
                  initial={profile}
                  onDone={() => {
                    setEditingId(null);
                    invalidate();
                  }}
                  onCancel={() => setEditingId(null)}
                />
              </li>
            ) : (
              <li key={profile.id} className="flex items-center gap-2 px-3 py-2.5">
                <span className="min-w-0 flex-1 truncate text-sm font-medium">{profile.name}</span>
                <button
                  type="button"
                  onClick={() => setEditingId(profile.id)}
                  className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                             hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                             focus-visible:outline-accent"
                >
                  Edit
                </button>
                <button
                  type="button"
                  aria-label={`Remove quality profile ${profile.name}`}
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
            No quality profile exists yet.
          </p>
        )
      )}

      {isTierVisible(tier, "expert") &&
        (creating ? (
          <div className="rounded-lg border border-border-subtle bg-surface p-3">
            <QualityProfileForm
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
            New quality profile
          </button>
        ))}
    </div>
  );
}

function QualityProfileForm({
  initial,
  onDone,
  onCancel,
}: {
  initial?: QualityProfile;
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [rules, setRules] = useState(initial?.rules ?? "{}");
  const [upgrade, setUpgrade] = useState(initial?.upgrade ?? "{}");

  const save = useMutation({
    mutationFn: async () => {
      const body = { name, rules, upgrade };
      const { error } = initial
        ? await api.PATCH("/qualityprofiles/{id}", { params: { path: { id: initial.id } }, body })
        : await api.POST("/qualityprofiles", { body });
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
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Rules (ADR-40's tier/target JSON)
        <textarea
          className={`${inputStyle} h-32 font-mono text-xs`}
          value={rules}
          spellCheck={false}
          onChange={(event) => setRules(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Upgrade policy JSON
        <textarea
          className={`${inputStyle} h-20 font-mono text-xs`}
          value={upgrade}
          spellCheck={false}
          onChange={(event) => setUpgrade(event.target.value)}
        />
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

/** Which profile this library uses, as cards. The shipped four are plain
 *  choices a normal user should be able to make; only editing the rules
 *  behind them is expert work (revision 9.2). Opened from a library's own
 *  config section, so there is a category to assign to -- reached any other
 *  way there is nothing to pick for and it stays hidden. */
function LibraryProfilePicker({ profiles }: { profiles: QualityProfile[] }) {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const queryClient = useQueryClient();

  const category = useQuery({
    queryKey: ["category", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}", {
        params: { path: { id: Number(categoryId) } },
      });
      if (error) throw error;
      return data;
    },
    enabled: categoryId !== undefined,
  });

  const choose = useMutation({
    mutationFn: async (profileId: number) => {
      // CategoryWrite is a wholesale write, so the fields this screen does not
      // own are sent back as they are rather than blanked.
      const current = category.data;
      if (current === undefined) return;
      const { error } = await api.PATCH("/categories/{id}", {
        params: { path: { id: Number(categoryId) } },
        body: {
          name: current.name,
          priority_rank: current.priority_rank,
          monitor_discovered: current.monitor_discovered,
          language_profile_id: current.language_profile_id,
          quality_profile_id: profileId,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["category", categoryId] });
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
    },
  });

  if (categoryId === undefined || !category.data) return null;
  const chosen = category.data.quality_profile_id;

  return (
    <section className="flex flex-col gap-2">
      <p className="text-sm leading-relaxed text-ink-muted">
        What this library should aim for. Everything it looks for is judged against this.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        {profiles.map((profile) => (
          <button
            key={profile.id}
            type="button"
            aria-pressed={chosen === profile.id}
            disabled={choose.isPending}
            onClick={() => choose.mutate(profile.id)}
            className={`flex flex-col items-start gap-0.5 rounded-lg border p-3 text-left text-sm
                        focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent ${
                          chosen === profile.id
                            ? "border-accent bg-accent/10"
                            : "border-border-subtle bg-surface hover:border-accent"
                        }`}
          >
            <span className="font-medium">{profile.name}</span>
            {chosen === profile.id && <span className="text-xs text-accent">In use</span>}
          </button>
        ))}
      </div>
      {choose.isError && <p className="text-xs text-danger">That could not be applied.</p>}
    </section>
  );
}
