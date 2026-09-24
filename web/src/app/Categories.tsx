import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Category = components["schemas"]["LibraryCategory"];
type CategorySettings = components["schemas"]["CategorySettings"];
type MediaType = components["schemas"]["MediaType"];
type MonitorDiscovered = components["schemas"]["CategorySettings"]["monitor_discovered"];

export const MEDIA_TYPES: readonly MediaType[] = [
  "music", "tv", "movie", "audiobook", "book", "adult", "comic", "rom",
  "podcast", "news", "youtube",
];

/** What a library of each media type is called when its own name is left
 *  blank -- "the default library name should always be the category, for
 *  example Music" (2026-09-19). Also what the media-type picker shows, so
 *  the two can never drift apart. */
export const MEDIA_TYPE_NAME: Record<MediaType, string> = {
  music: "Music", tv: "TV", movie: "Movies", audiobook: "Audiobooks", book: "Books",
  adult: "Adult", comic: "Comics", rom: "ROMs", podcast: "Podcasts", news: "News",
  youtube: "YouTube",
};

const MONITOR_DISCOVERED_OPTIONS: readonly MonitorDiscovered[] = ["monitored", "unmonitored", "none"];

/** The policy boundary for naming, profiles and access (ADR-27). Section 21.14's
 *  "no POST /categories at all" gap: standing one up used to need a hand-written
 *  settings/import YAML document. Editing (todo 4.6's own remaining gap, landed
 *  2026-09-12) reads/writes CategorySettings, a richer administrator-only shape
 *  than the LibraryCategory this list itself renders from -- see GetCategory's
 *  own doc comment for why the two stay separate. */
export function Categories() {
  const queryClient = useQueryClient();
  const [editingId, setEditingId] = useState<number | null>(null);
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["categories"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/categories/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const [searchMessages, setSearchMessages] = useState<Record<number, string>>({});
  const search = useMutation({
    mutationFn: async (id: number) => {
      const { data, error } = await api.POST("/categories/{id}/search", { params: { path: { id } } });
      if (error) throw error;
      return { id, duplicate: data!.duplicate };
    },
    onSuccess: ({ id, duplicate }) =>
      setSearchMessages((prev) => ({
        ...prev,
        [id]: duplicate ? "Already running" : "Search started",
      })),
  });

  // A non-administrator cannot read this list at all (403); render nothing
  // rather than an error, the same convention Users.tsx uses.
  if (categories.error !== null) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Libraries</h2>

      {categories.data && categories.data.length > 0 ? (
        <ul className="flex flex-col divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {categories.data.map((category) =>
            editingId === category.id ? (
              <li key={category.id} className="p-3">
                <EditCategoryForm
                  categoryId={category.id}
                  onDone={() => {
                    setEditingId(null);
                    invalidate();
                  }}
                  onCancel={() => setEditingId(null)}
                />
              </li>
            ) : (
              <Row
                key={category.id}
                category={category}
                onEdit={() => setEditingId(category.id)}
                onRemove={() => remove.mutate(category.id)}
                busy={remove.isPending}
                onSearch={() => search.mutate(category.id)}
                searchPending={search.isPending && search.variables === category.id}
                searchMessage={searchMessages[category.id]}
              />
            ),
          )}
        </ul>
      ) : (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          No category is configured yet.
        </p>
      )}

      {remove.isError && (
        <p className="text-xs text-danger">
          That category could not be removed -- it is still referenced by a root folder, a
          user's access grant, or a discovery job.
        </p>
      )}

      <AddCategory onAdded={invalidate} />
    </section>
  );
}

function Row({
  category,
  onEdit,
  onRemove,
  busy,
  onSearch,
  searchPending,
  searchMessage,
}: {
  category: Category;
  onEdit: () => void;
  onRemove: () => void;
  busy: boolean;
  onSearch: () => void;
  searchPending: boolean;
  searchMessage: string | undefined;
}) {
  return (
    <li className="flex items-center gap-3 px-3 py-2.5">
      <span className="text-sm font-medium">{category.name}</span>
      <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem]
                       text-ink-muted">
        {category.media_type}
      </span>
      {category.works != null && (
        <span className="ml-auto shrink-0 text-xs text-ink-muted">{category.works} works</span>
      )}
      {category.media_type === "music" && (
        <>
          {searchMessage && <span className="text-xs text-ink-muted">{searchMessage}</span>}
          <button
            type="button"
            onClick={onSearch}
            disabled={searchPending}
            title="Search every monitored, incomplete album in this library"
            className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                       hover:text-ink disabled:opacity-50 focus-visible:outline-2
                       focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {searchPending ? "…" : "Search"}
          </button>
        </>
      )}
      <button
        type="button"
        onClick={onEdit}
        className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                   hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        Edit
      </button>
      <button
        type="button"
        aria-label={`Remove ${category.name}`}
        onClick={onRemove}
        disabled={busy}
        className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </button>
    </li>
  );
}

// Lazy-loaded on "Edit": the list itself renders from the lean LibraryCategory,
// which carries none of priority_rank/the two profile ids/monitor_discovered.
export function EditCategoryForm({
  categoryId,
  onDone,
  onCancel,
}: {
  categoryId: number;
  onDone: () => void;
  onCancel: () => void;
}) {
  const detail = useQuery({
    queryKey: ["category", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}", { params: { path: { id: categoryId } } });
      if (error) throw error;
      return data;
    },
  });
  const qualityProfiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const languageProfiles = useQuery({
    queryKey: ["languageProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/languageprofiles");
      if (error) throw error;
      return data;
    },
  });

  if (detail.isLoading) return <p className="text-xs text-ink-muted">Loading…</p>;
  if (detail.error !== null || !detail.data) {
    return <p className="text-xs text-danger">This library could not be read.</p>;
  }

  return (
    <EditCategoryFields
      categoryId={categoryId}
      initial={detail.data}
      qualityProfiles={qualityProfiles.data ?? []}
      languageProfiles={languageProfiles.data ?? []}
      onDone={onDone}
      onCancel={onCancel}
    />
  );
}

function EditCategoryFields({
  categoryId,
  initial,
  qualityProfiles,
  languageProfiles,
  onDone,
  onCancel,
}: {
  categoryId: number;
  initial: CategorySettings;
  qualityProfiles: components["schemas"]["QualityProfile"][];
  languageProfiles: components["schemas"]["LanguageProfile"][];
  onDone: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial.name);
  const [priorityRank, setPriorityRank] = useState(initial.priority_rank);
  const [qualityProfileId, setQualityProfileId] = useState(initial.quality_profile_id ?? 0);
  const [languageProfileId, setLanguageProfileId] = useState(initial.language_profile_id ?? 0);
  const [monitorDiscovered, setMonitorDiscovered] = useState<MonitorDiscovered>(initial.monitor_discovered);
  const queryClient = useQueryClient();

  const save = useMutation({
    mutationFn: async () => {
      const { error } = await api.PATCH("/categories/{id}", {
        params: { path: { id: categoryId } },
        body: {
          name,
          priority_rank: priorityRank,
          quality_profile_id: qualityProfileId || undefined,
          language_profile_id: languageProfileId || undefined,
          monitor_discovered: monitorDiscovered,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      // The sidebar tree reads "libraries", not "categories"; a rename must reach both.
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
      void queryClient.invalidateQueries({ queryKey: ["category", categoryId] });
      onDone();
    },
  });

  return (
    <form
      className="flex flex-col gap-2"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate();
      }}
    >
      <div className="grid grid-cols-2 gap-2">
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
          Priority rank
          <input
            className={inputStyle}
            type="number"
            min={0}
            value={priorityRank}
            onChange={(event) => setPriorityRank(Number(event.target.value))}
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Quality profile
          <select
            className={inputStyle}
            value={qualityProfileId}
            onChange={(event) => setQualityProfileId(Number(event.target.value))}
          >
            <option value={0}>None</option>
            {qualityProfiles.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Language profile
          <select
            className={inputStyle}
            value={languageProfileId}
            onChange={(event) => setLanguageProfileId(Number(event.target.value))}
          >
            <option value={0}>None</option>
            {languageProfiles.map((profile) => (
              <option key={profile.id} value={profile.id}>
                {profile.name}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Monitor discovered
          <select
            className={inputStyle}
            value={monitorDiscovered}
            onChange={(event) => setMonitorDiscovered(event.target.value as MonitorDiscovered)}
          >
            {MONITOR_DISCOVERED_OPTIONS.map((option) => (
              <option key={option} value={option}>
                {option}
              </option>
            ))}
          </select>
        </label>
      </div>
      {save.isError && (
        <p className="text-xs text-danger">
          {(save.error as { detail?: string }).detail ?? "That library could not be saved."}
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

function AddCategory({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("");
  const [mediaType, setMediaType] = useState<MediaType>("music");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/categories", {
        body: { name: name.trim() || MEDIA_TYPE_NAME[mediaType], media_type: mediaType },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      setName("");
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
      <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Name
        <input
          className={inputStyle}
          value={name}
          autoComplete="off"
          placeholder={MEDIA_TYPE_NAME[mediaType]}
          onChange={(event) => setName(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Media type
        <select
          className={inputStyle}
          value={mediaType}
          onChange={(event) => setMediaType(event.target.value as MediaType)}
        >
          {MEDIA_TYPES.map((type) => (
            <option key={type} value={type}>
              {MEDIA_TYPE_NAME[type]}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Adding…" : "Add library"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That category could not be created. Check the name is not empty.
        </p>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
