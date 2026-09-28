import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { ListTable, type Column } from "@/list/ListTable";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";
import type { components } from "@/api/schema";

type UnidentifiedFile = components["schemas"]["UnidentifiedFile"];

// F29: what a scan could not confidently resolve. Confirm takes the best candidate here; the
// Review page picks another, searches for the album by hand, or marks the file not media.
const FILTER_KEYS: readonly string[] = ["category_id"];

export function Unidentified() {
  const queryClient = useQueryClient();
  const list = useLibraryList<UnidentifiedFile>(
    {
      name: "unidentified",
      idOf: (file) => file.media_object_id,
      fetchPage: async ({ cursor, query, signal }) => {
        const categoryId = query.filters.category_id;
        const { data, error } = await api.GET("/library/unidentified", {
          params: {
            query: {
              cursor,
              limit: 100,
              category_id: categoryId ? Number(categoryId) : undefined,
            },
          },
          signal,
        });
        if (error) throw error;
        return data;
      },
    },
    useRouteView(FILTER_KEYS),
  );

  const resolve = useMutation({
    mutationFn: async (file: UnidentifiedFile) => {
      const best = file.candidates[0];
      if (best?.provider === undefined || best.external_id === undefined) return;
      const { error } = await api.POST("/library/unidentified/{mediaObjectId}/resolve", {
        params: { path: { mediaObjectId: file.media_object_id } },
        body: { provider: best.provider, external_id: best.external_id },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["unidentified"] });
    },
  });

  const columns: readonly Column<UnidentifiedFile>[] = [
    {
      key: "rel_path",
      header: "Path",
      render: (file) => (
        <Link
          to="/unidentified/$mediaObjectId"
          params={{ mediaObjectId: String(file.media_object_id) }}
          className="hover:underline"
        >
          {file.rel_path}
        </Link>
      ),
    },
    {
      key: "candidates",
      header: "Best guess",
      render: (file) => (file.candidates.length > 0 ? describeBest(file) : "No match found"),
    },
    { key: "first_seen_at", header: "First seen", width: "12rem", render: (file) => file.first_seen_at },
    {
      key: "actions",
      header: "",
      width: "13rem",
      render: (file) => (
        <span className="flex items-center gap-1">
          <Link
            to="/unidentified/$mediaObjectId"
            params={{ mediaObjectId: String(file.media_object_id) }}
            className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted hover:text-ink
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Review
          </Link>
          <ResolveAction file={file} onConfirm={() => resolve.mutate(file)} />
        </span>
      ),
    },
  ];

  return (
    <div className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Unidentified</h2>
        <span className="text-xs text-ink-muted">
          Files a scan found but could not confidently match to a work
        </span>
      </div>

      <div className="min-h-0 flex-1">
        {list.isLoading && <Message>Loading the queue…</Message>}
        {list.error !== null && <Message>The queue could not be read.</Message>}
        {!list.isLoading && list.error === null && list.items.length === 0 && (
          <Message>Nothing here -- every scanned file identified confidently.</Message>
        )}

        {list.items.length > 0 && (
          <ListTable list={list} columns={columns} idOf={(file) => file.media_object_id} />
        )}
      </div>
    </div>
  );
}

/** Only a musicbrainz:release-group candidate can be confirmed today
 *  (internal/media/music.ErrUnsupportedProvider) -- other strategies (hash,
 *  fingerprint) do not carry a provider id the resolve action can look a
 *  title up from. */
function ResolveAction({ file, onConfirm }: { file: UnidentifiedFile; onConfirm: () => void }) {
  const [confirming, setConfirming] = useState(false);
  const best = file.candidates[0];
  if (best?.provider !== "musicbrainz:release-group" || best.external_id === undefined) return null;

  if (!confirming) {
    return (
      <button
        type="button"
        onClick={() => setConfirming(true)}
        className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted
                   hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        Confirm
      </button>
    );
  }
  return (
    <span className="flex items-center gap-1 text-xs">
      <button
        type="button"
        onClick={() => {
          setConfirming(false);
          onConfirm();
        }}
        className="rounded-md bg-accent px-2 py-0.5 text-accent-ink focus-visible:outline-2
                   focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Yes, this one
      </button>
      <button
        type="button"
        onClick={() => setConfirming(false)}
        className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink"
      >
        Cancel
      </button>
    </span>
  );
}

// The ranked candidates are best-first (model.RunChain, ADR-24); the manual
// queue only needs the strongest guess at a glance, not the full list --
// that depth belongs to the full item page (§21.3), not this row.
function describeBest(file: UnidentifiedFile): string {
  const best = file.candidates[0];
  if (best === undefined) return "No match found";
  const confidence = Math.round(best.confidence * 100);
  return `${best.provider ?? best.strategy} (${confidence}%)`;
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
