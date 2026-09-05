import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Category = components["schemas"]["LibraryCategory"];
type MediaType = components["schemas"]["MediaType"];

const MEDIA_TYPES: readonly MediaType[] = [
  "music", "tv", "movie", "audiobook", "book", "adult", "comic", "rom",
  "podcast", "news", "youtube",
];

/** The policy boundary for naming, profiles and access (ADR-27). Section 21.14's
 *  "no POST /categories at all" gap: standing one up used to need a hand-written
 *  settings/import YAML document. */
export function Categories() {
  const queryClient = useQueryClient();
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

  // A non-administrator cannot read this list at all (403); render nothing
  // rather than an error, the same convention Users.tsx uses.
  if (categories.error !== null) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Categories</h2>

      {categories.data && categories.data.length > 0 ? (
        <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
          {categories.data.map((category) => (
            <Row
              key={category.id}
              category={category}
              onRemove={() => remove.mutate(category.id)}
              busy={remove.isPending}
            />
          ))}
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
  onRemove,
  busy,
}: {
  category: Category;
  onRemove: () => void;
  busy: boolean;
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

function AddCategory({ onAdded }: { onAdded: () => void }) {
  const [name, setName] = useState("");
  const [mediaType, setMediaType] = useState<MediaType>("music");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/categories", {
        body: { name, media_type: mediaType },
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
              {type}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={!name.trim() || create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Adding…" : "Add category"}
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
