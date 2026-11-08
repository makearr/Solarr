import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { kindSecret } from "@/lib/downloaderKinds";
import { useDownloaderKinds } from "@/lib/resources";

import { ClientOptionsForm } from "./Downloaders";
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
      {app === "prowlarr" && <ProwlarrSync instanceId={instanceId} />}
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

/** A client of the connection that is already added here, with an Edit button for its options and password. */
function ExistingClient({ baseURL }: { baseURL: string }) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const kinds = useDownloaderKinds();
  const downloaders = useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });
  const trimmed = baseURL.replace(/\/+$/, "");
  const here = downloaders.data?.find((one) => one.base_url.replace(/\/+$/, "") === trimmed);
  return (
    <>
      <span className="text-ink-muted">already added</span>
      {here && (
        <button type="button" onClick={() => setEditing(!editing)} className={button}>
          {editing ? "Close" : "Edit"}
        </button>
      )}
      {here && editing && (
        <div className="basis-full">
          <ClientOptionsForm
            downloader={here}
            kind={kinds.data?.find((one) => one.impl === here.impl)}
            onDone={() => {
              setEditing(false);
              void queryClient.invalidateQueries({ queryKey: ["downloaders"] });
              void queryClient.invalidateQueries({ queryKey: ["arr-downloadclients"] });
            }}
          />
        </div>
      )}
    </>
  );
}

function DownloadClientRow({ client, instanceId, keepCategory }: { client: ArrDownloadClient; instanceId: number; keepCategory: boolean }) {
  const queryClient = useQueryClient();
  const [key, setKey] = useState("");
  const kinds = useDownloaderKinds();
  const kind = kinds.data?.find((one) => one.impl === client.impl);
  const withUser = kind?.user_name ?? false;
  const [username, setUsername] = useState(client.username ?? "");
  const add = useMutation({
    mutationFn: async () => {
      if (!kind) return;
      const { error } = await api.POST("/downloaders", {
        body: {
          name: client.name, impl: kind.impl, base_url: client.base_url, protocol: kind.protocol,
          credentials: kindSecret(kind, username, key) || undefined,
          // Without its password a client that signs in stays switched off; Edit sets the password, then switch it on.
          enabled: client.enabled && !(kind.secret_label && !key.trim()),
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
        <ExistingClient baseURL={client.base_url} />
      ) : !client.supported || !kind ? (
        <span className="text-warn">not supported yet</span>
      ) : !kind.has_address ? (
        <span className="text-ink-muted">add it under Download clients, with its folders</span>
      ) : (
        <>
          {withUser && (
            <input
              autoComplete="off"
              aria-label={`${client.name} user name`}
              placeholder="User name"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              className={`${input} w-40`}
            />
          )}
          {kind.secret_label && (
            <input
              type="password"
              autoComplete="off"
              aria-label={`${client.name} ${kind.secret_label}`}
              placeholder={`${kind.secret_label} (the *Arr hides it; leave empty to add it switched off)`}
              value={key}
              onChange={(event) => setKey(event.target.value)}
              className={`${input} w-64`}
            />
          )}
          <button
            type="button"
            disabled={add.isPending}
            onClick={() => add.mutate()}
            className={button}
          >
            {add.isPending ? "Adding…" : kind.secret_label && !key.trim() ? "Add without password" : "Add"}
          </button>
          {add.isError && <span className="text-danger">{(add.error as { detail?: string }).detail ?? "It could not be added."}</span>}
        </>
      )}
    </div>
  );
}

type SyncOptions = components["schemas"]["ProwlarrSyncOptions"];

const syncChoices: { key: keyof SyncOptions; label: string }[] = [
  { key: "active_indexers", label: "Active indexers" },
  { key: "paused_indexers", label: "Paused indexers (added paused)" },
  { key: "supported_only", label: "Only indexers with categories Solarr serves" },
  { key: "download_clients", label: "Active download clients" },
  { key: "proxies", label: "Proxies the indexers use (FlareSolverr)" },
  { key: "automatic", label: "Keep in step every hour" },
  { key: "prowlarr_as_indexer", label: "Use Prowlarr itself as the indexer (off: each indexer is the site behind it)" },
];

/** A FlareSolverr address added to the proxy list on the next sync; each indexer then picks it under its own proxy. */
function FlareSolverrField({ value, disabled, onSave }: { value: string; disabled: boolean; onSave: (url: string) => void }) {
  const [draft, setDraft] = useState(value);
  return (
    <label className="flex flex-wrap items-center gap-2 text-xs">
      FlareSolverr
      <input
        type="url"
        placeholder="http://flaresolverr:8191"
        value={draft}
        disabled={disabled}
        onChange={(event) => setDraft(event.target.value)}
        className="w-64 rounded-md border border-border-subtle bg-canvas px-2 py-1 font-mono"
      />
      <button type="button" disabled={disabled || draft === value} onClick={() => onSave(draft.trim())} className={button}>
        Save
      </button>
      <span className="text-ink-muted">Added as a proxy; choose it per indexer.</span>
    </label>
  );
}

/** What Solarr keeps in step with this Prowlarr, and a button to do it now. */
function ProwlarrSync({ instanceId }: { instanceId: number }) {
  const queryClient = useQueryClient();
  const options = useQuery({
    queryKey: ["arr-sync-options", instanceId],
    queryFn: () => ok(api.GET("/arr/instances/{id}/prowlarr-sync", { params: { path: { id: instanceId } } })),
  });
  const save = useMutation({
    mutationFn: (body: SyncOptions) => ok(api.PUT("/arr/instances/{id}/prowlarr-sync", { params: { path: { id: instanceId } }, body })),
    onSuccess: (stored) => queryClient.setQueryData(["arr-sync-options", instanceId], stored),
  });
  const sync = useMutation({
    mutationFn: () => ok(api.POST("/arr/instances/{id}/prowlarr-sync", { params: { path: { id: instanceId } } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["indexers"] });
      void queryClient.invalidateQueries({ queryKey: ["arr-indexers", instanceId] });
    },
  });
  return (
    <section className="flex flex-col gap-1.5 text-sm">
      <h3 className="text-xs font-medium text-ink-muted">Keep in step with Prowlarr</h3>
      {syncChoices.map((choice) => (
        <label key={choice.key} className="flex items-center gap-2 text-xs">
          <input
            type="checkbox"
            checked={options.data?.[choice.key] === true}
            disabled={!options.data || save.isPending}
            onChange={(event) => options.data && save.mutate({ ...options.data, [choice.key]: event.target.checked })}
          />
          {choice.label}
        </label>
      ))}
      <FlareSolverrField
        key={options.data?.flaresolverr_url ?? ""}
        value={options.data?.flaresolverr_url ?? ""}
        disabled={!options.data || save.isPending}
        onSave={(url) => options.data && save.mutate({ ...options.data, flaresolverr_url: url })}
      />
      <button type="button" disabled={sync.isPending} onClick={() => sync.mutate()} className={button}>
        {sync.isPending ? "Syncing…" : "Sync now"}
      </button>
      {sync.isSuccess && (
        <p className="text-xs text-ink-muted">
          {sync.data.added} added, {sync.data.updated} updated, {sync.data.paused} paused, {sync.data.resumed} resumed,
          {" "}{sync.data.skipped} left out. They are under Connections → Indexers.
          {(sync.data.clients_added ?? 0) > 0 && ` ${sync.data.clients_added} download client(s) added.`}
          {(sync.data.clients_waiting ?? 0) > 0 && ` ${sync.data.clients_waiting} more need a password or key: use "Use its download clients…".`}
        </p>
      )}
      {sync.isError && <p className="text-xs text-danger">{(sync.error as { detail?: string }).detail ?? "The sync failed."}</p>}
    </section>
  );
}
