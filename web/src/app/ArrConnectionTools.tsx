import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { joinCredentials } from "@/lib/downloadCredentials";
import type { components } from "@/api/schema";

type ArrApp = components["schemas"]["ArrApp"];
type ArrDownloadClient = components["schemas"]["ArrDownloadClient"];

const button =
  "w-fit rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50";
const input = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs";

/** What a connection offers besides its library (asked 2026-09-30): Prowlarr's active indexers, and any
 *  connection's download clients. Prowlarr's key stays on the server; a client's own key is asked once,
 *  since the *Arr sends it masked. */
export function ArrConnectionTools({ instanceId, app }: { instanceId: number; app: ArrApp }) {
  return (
    <div className="flex flex-col gap-3 border-t border-border-subtle pt-2">
      {app === "prowlarr" && <UseIndexers instanceId={instanceId} />}
      {(app === "prowlarr" || app === "lidarr") && <UseDownloadClients instanceId={instanceId} app={app} />}
    </div>
  );
}

function UseIndexers({ instanceId }: { instanceId: number }) {
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [chosen, setChosen] = useState<Set<number> | null>(null);
  const found = useQuery({
    queryKey: ["arr-indexers", instanceId],
    enabled: open,
    queryFn: () => ok(api.GET("/arr/instances/{id}/indexers", { params: { path: { id: instanceId } } })),
  });
  // Offered by default, as the Indexers page does: enabled usenet indexers with music, not yet used.
  const offered = (found.data ?? []).filter((entry) => entry.enabled && entry.music && !entry.already_added && entry.impl === "newznab");
  const selected = chosen ?? new Set(offered.map((entry) => entry.prowlarr_id));
  const use = useMutation({
    mutationFn: () => ok(api.POST("/arr/instances/{id}/indexers", {
      params: { path: { id: instanceId } },
      body: { prowlarr_ids: [...selected] },
    })),
    onSuccess: () => {
      setChosen(null);
      void queryClient.invalidateQueries({ queryKey: ["arr-indexers", instanceId] });
      void queryClient.invalidateQueries({ queryKey: ["indexers"] });
    },
  });

  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className={button}>
        Use its active indexers…
      </button>
    );
  }
  return (
    <section className="flex flex-col gap-1.5 text-sm">
      <h3 className="text-xs font-medium text-ink-muted">Indexers</h3>
      {found.isLoading && <p className="text-xs text-ink-muted">Reading Prowlarr…</p>}
      {found.isError && <p className="text-xs text-danger">{(found.error as { detail?: string }).detail ?? "Prowlarr could not be read."}</p>}
      {found.data?.map((entry) => (
        <label key={entry.prowlarr_id} className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            disabled={entry.already_added}
            checked={entry.already_added || selected.has(entry.prowlarr_id)}
            onChange={(event) => {
              const next = new Set(selected);
              if (event.target.checked) next.add(entry.prowlarr_id);
              else next.delete(entry.prowlarr_id);
              setChosen(next);
            }}
          />
          <span className="text-ink">{entry.name}</span>
          <span className="text-ink-muted">
            {entry.impl === "torznab" ? "torrent: no torrent client yet" : "usenet"}
            {!entry.enabled && " · disabled in Prowlarr"}
            {!entry.music && " · no music categories"}
            {entry.already_added && " · already used"}
          </span>
        </label>
      ))}
      {found.data && (
        <button type="button" disabled={selected.size === 0 || use.isPending} onClick={() => use.mutate()} className={button}>
          {use.isPending ? "Adding…" : `Use ${selected.size} indexer${selected.size === 1 ? "" : "s"}`}
        </button>
      )}
      {use.isSuccess && <p className="text-xs text-ink-muted">{use.data.added} added under Connections → Indexers.</p>}
      {use.isError && <p className="text-xs text-danger">Not every indexer could be added.</p>}
    </section>
  );
}

function UseDownloadClients({ instanceId, app }: { instanceId: number; app: ArrApp }) {
  const [open, setOpen] = useState(false);
  const found = useQuery({
    queryKey: ["arr-downloadclients", instanceId],
    enabled: open,
    queryFn: () => ok(api.GET("/arr/instances/{id}/downloadclients", { params: { path: { id: instanceId } } })),
  });
  if (!open) {
    return (
      <button type="button" onClick={() => setOpen(true)} className={button}>
        Use its download clients…
      </button>
    );
  }
  return (
    <section className="flex flex-col gap-1.5 text-sm">
      <h3 className="text-xs font-medium text-ink-muted">Download clients</h3>
      {found.isLoading && <p className="text-xs text-ink-muted">Reading…</p>}
      {found.isError && <p className="text-xs text-danger">{(found.error as { detail?: string }).detail ?? "The connection could not be read."}</p>}
      {found.data?.length === 0 && <p className="text-xs text-ink-muted">It has none.</p>}
      {found.data?.map((client) => (
        <DownloadClientRow key={`${client.name}-${client.base_url}`} client={client} instanceId={instanceId} keepCategory={app === "lidarr"} />
      ))}
    </section>
  );
}

function DownloadClientRow({ client, instanceId, keepCategory }: { client: ArrDownloadClient; instanceId: number; keepCategory: boolean }) {
  const queryClient = useQueryClient();
  const [key, setKey] = useState("");
  const torrent = client.impl === "qbittorrent";
  const [username, setUsername] = useState(client.username ?? "");
  const add = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/downloaders", {
        body: {
          name: client.name, impl: torrent ? "qbittorrent" : "sabnzbd", base_url: client.base_url, protocol: torrent ? "torrent" : "usenet",
          credentials: torrent ? joinCredentials(username, key) : key, enabled: client.enabled,
          // Lidarr's category is a music one; Prowlarr's is for its own grabs, so Solarr's default applies.
          options: { category: keepCategory ? client.category : undefined },
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["arr-downloadclients", instanceId] });
      void queryClient.invalidateQueries({ queryKey: ["downloaders"] });
    },
  });
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <span className="text-ink">{client.name}</span>
      <span className="font-mono text-ink-muted">{client.base_url}</span>
      <span className="text-ink-muted">{client.impl}{client.category ? ` · ${client.category}` : ""}</span>
      {client.already_added ? (
        <span className="text-ink-muted">already added</span>
      ) : !client.supported ? (
        <span className="text-warn">not supported yet</span>
      ) : (
        <>
          {torrent && (
            <input
              autoComplete="off"
              aria-label={`${client.name} user name`}
              placeholder="User name"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              className={`${input} w-40`}
            />
          )}
          <input
            type="password"
            autoComplete="off"
            aria-label={torrent ? `${client.name} password` : `${client.name} API key`}
            placeholder={torrent ? "Its password (the *Arr hides it)" : "Its API key (the *Arr hides it)"}
            value={key}
            onChange={(event) => setKey(event.target.value)}
            className={`${input} w-64`}
          />
          <button type="button" disabled={(!torrent && !key.trim()) || add.isPending} onClick={() => add.mutate()} className={button}>
            {add.isPending ? "Adding…" : "Add"}
          </button>
          {add.isError && <span className="text-danger">{(add.error as { detail?: string }).detail ?? "It could not be added."}</span>}
        </>
      )}
    </div>
  );
}
