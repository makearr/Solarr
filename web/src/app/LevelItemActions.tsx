import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Bookmark, BookmarkCheck, RefreshCw, Search } from "lucide-react";

import { api } from "@/api/client";

import { EditAlbumButton } from "./EditAlbum";

/** Radarr's per-item actions on an album or artist (asked 2026-09-30): monitor, search, refresh.
 *  Every click stops at the button, since the tile or row around it is a link. */
export function useItemActions(level: string | undefined, id: number, monitored: boolean | undefined) {
  const queryClient = useQueryClient();
  const [note, setNote] = useState<string>();
  const album = level === "albums";
  const refresh = useMutation({
    mutationFn: async () => {
      const { error } = album
        ? await api.POST("/works/{id}/refresh", { params: { path: { id } } })
        : await api.POST("/people/{id}/refresh", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: () => setNote("Refreshing"),
    onError: () => setNote("Refresh failed"),
  });
  const search = useMutation({
    mutationFn: async () => {
      if (album) {
        const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id } } });
        if (error) throw error;
        return data.sent ? "Sent to the download client" : data.found > 0 ? "Found, none acceptable" : "Nothing found";
      }
      const { error } = await api.POST("/people/{id}/search", { params: { path: { id } } });
      if (error) throw error;
      return "Searching missing albums";
    },
    onMutate: () => setNote("Searching…"),
    onSuccess: (message) => setNote(message),
    onError: () => setNote("Search failed"),
  });
  const monitor = useMutation({
    mutationFn: async (next: boolean) => {
      const { error } = await api.POST("/works/{id}/monitor", { params: { path: { id } }, body: { monitored: next } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ predicate: (query) => String(query.queryKey[0]).startsWith("library-level") }),
    onError: () => setNote("Monitoring could not be changed"),
  });
  return { album, id, refresh, search, monitor, monitored, note };
}

type Actions = ReturnType<typeof useItemActions>;

const stop = (event: React.MouseEvent, run: () => void) => {
  event.preventDefault();
  event.stopPropagation();
  run();
};

/** The monitored bookmark, Radarr's top-left corner; albums only. */
export function MonitorToggle({ actions, className = "" }: { actions: Actions; className?: string }) {
  if (!actions.album || actions.monitored === undefined) return null;
  const Icon = actions.monitored ? BookmarkCheck : Bookmark;
  const label = actions.monitored ? "Monitored: click to stop" : "Not monitored: click to monitor";
  return (
    <button
      type="button"
      title={label}
      aria-label={label}
      disabled={actions.monitor.isPending}
      onClick={(event) => stop(event, () => actions.monitor.mutate(!actions.monitored))}
      className={`flex size-7 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 shadow-sm
                  ${actions.monitored ? "text-accent" : "text-ink-muted"} hover:text-ink disabled:opacity-50 ${className}`}
    >
      <Icon aria-hidden className="size-4" />
    </button>
  );
}

/** Search and refresh as small buttons; the note says what the last one did. */
export function ActionButtons({ actions, showNote = true }: { actions: Actions; showNote?: boolean }) {
  const button =
    "flex size-7 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 text-ink-muted shadow-sm hover:text-ink disabled:opacity-50";
  const searchLabel = actions.album ? "Search: send the best release" : "Search all missing albums";
  return (
    <span className="flex items-center gap-1">
      <button
        type="button"
        title={searchLabel}
        aria-label={searchLabel}
        disabled={actions.search.isPending}
        onClick={(event) => stop(event, () => actions.search.mutate())}
        className={button}
      >
        <Search aria-hidden className="size-3.5" />
      </button>
      <button
        type="button"
        title="Refresh metadata"
        aria-label="Refresh metadata"
        disabled={actions.refresh.isPending}
        onClick={(event) => stop(event, () => actions.refresh.mutate())}
        className={button}
      >
        <RefreshCw aria-hidden className={`size-3.5 ${actions.refresh.isPending ? "animate-spin" : ""}`} />
      </button>
      {actions.album && <EditAlbumButton id={actions.id} />}
      {showNote && actions.note && <span className="truncate text-xs text-ink-muted">{actions.note}</span>}
    </span>
  );
}
