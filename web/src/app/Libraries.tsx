import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus } from "lucide-react";

import { api } from "@/api/client";
import { mediaTypeIcon } from "@/lib/mediaTypeIcon";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

import { MEDIA_TYPES } from "./Categories";
import { defaultLevelKey } from "./Sidebar";

type LibraryEntry = components["schemas"]["Library"];
type MediaType = components["schemas"]["MediaType"];

/** The Library section's own landing page (asked for 2026-09-13): the header
 *  in the sidebar is a link now, and this is where it lands -- every library
 *  as a card, and the only place a new one can be created from the interface.
 *  A library is a category (ADR-54), so creating one is POST /categories. */
export function Libraries() {
  const tier = useTier();
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="flex h-full flex-col gap-4 overflow-auto p-6">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">Libraries</h1>
        <p className="text-sm text-ink-muted">Every collection MediaNexus manages.</p>
      </div>

      {libraries.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {libraries.error != null && <p className="text-sm text-danger">The libraries could not be read.</p>}

      {libraries.data && libraries.data.length === 0 && (
        <p className="text-sm text-ink-muted">
          No libraries yet.{" "}
          {isTierVisible(tier, "advanced")
            ? "Add one below to get started."
            : "Switch to the advanced level in Settings → User to add one."}
        </p>
      )}

      {libraries.data && libraries.data.length > 0 && (
        <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {libraries.data.map((library) => (
            <LibraryCard key={library.id} library={library} />
          ))}
        </ul>
      )}

      {isTierVisible(tier, "advanced") && <AddLibrary />}
    </div>
  );
}

function LibraryCard({ library }: { library: LibraryEntry }) {
  const Icon = mediaTypeIcon(library.media_type);
  const level = defaultLevelKey(library);
  const content = (
    <>
      <span className="flex items-center gap-2">
        <Icon aria-hidden className="size-5 shrink-0 text-accent" />
        <span className="truncate font-medium">{library.name}</span>
        <span className="ml-auto shrink-0 text-xs uppercase tracking-wide text-ink-muted">{library.media_type}</span>
      </span>
      <span className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-ink-muted">
        {library.levels.map((entry) => (
          <span key={entry.key}>
            {entry.label}
            {entry.count != null && <span className="ml-1 text-ink">{entry.count}</span>}
          </span>
        ))}
      </span>
    </>
  );

  return (
    <li>
      {level ? (
        <Link
          to="/library/$categoryId/$level"
          params={{ categoryId: String(library.id), level }}
          className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4
                     hover:border-accent focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          {content}
        </Link>
      ) : (
        <div className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">{content}</div>
      )}
    </li>
  );
}

function AddLibrary() {
  const queryClient = useQueryClient();
  const [name, setName] = useState("");
  const [mediaType, setMediaType] = useState<MediaType>("music");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/categories", { body: { name, media_type: mediaType } });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      setName("");
      void queryClient.invalidateQueries();
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
          placeholder="Music"
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
        className="flex items-center gap-1.5 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Plus aria-hidden className="size-4" />
        {create.isPending ? "Adding…" : "Add library"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">That library could not be created. Check the name is not empty.</p>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
