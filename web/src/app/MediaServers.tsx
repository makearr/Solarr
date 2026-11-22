import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { useCategories } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { RemoveButton } from "@/ui/RemoveButton";
import { inputStyle, labelStyle } from "@/ui/form";

type MediaServer = components["schemas"]["MediaServer"];
type Kind = components["schemas"]["MediaServerKind"];
type TestResult = components["schemas"]["MediaServerTestResult"];
type PathPair = components["schemas"]["MediaServerPathPair"];

const SERVERS_KEY = ["media-servers"];

/** What a connection is used for, as chips: only what the person switched on. */
export function usesOf(server: Pick<MediaServer, "as_metadata" | "push_files" | "announce" | "sync_playback">): string[] {
  const uses: string[] = [];
  if (server.as_metadata) uses.push(t("mediaServers.useMetadata"));
  if (server.push_files) uses.push(t("mediaServers.usePush"));
  if (server.announce) uses.push(t("mediaServers.useAnnounce"));
  if (server.sync_playback) uses.push(t("mediaServers.usePlayback"));
  return uses;
}

/** Connections > Media servers: Jellyfin, Navidrome and the like, told about new files and read for metadata and watch state. */
export function MediaServers() {
  const navigate = useNavigate();
  const { edit } = useSearch({ strict: false }) as { edit?: string };
  const queryClient = useQueryClient();
  const kinds = useQuery({ queryKey: ["media-servers", "kinds"], queryFn: () => ok(api.GET("/media-servers/kinds")) });
  const servers = useQuery({ queryKey: SERVERS_KEY, queryFn: () => ok(api.GET("/media-servers")) });
  const remove = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (id: number) => ok(api.DELETE("/media-servers/{id}", { params: { path: { id } } })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: SERVERS_KEY }),
  });
  const kindOf = (key: string) => kinds.data?.find((kind) => kind.key === key);
  const open = (value: string | undefined) => void navigate({ to: "/connections/media-servers", search: value ? { edit: value } : {} });
  const editing = edit === "new" ? "new" : servers.data?.find((server) => String(server.id) === edit);

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">{t("mediaServers.title")}</h1>
        <p className="text-sm text-ink-muted">{t("mediaServers.intro")}</p>
      </header>
      {servers.isLoading && <p className="text-sm text-ink-muted">{t("mediaServers.loading")}</p>}
      {servers.isError && <p className="text-sm text-danger">{t("mediaServers.couldNotRead")}</p>}
      {servers.data && servers.data.length === 0 && editing === undefined && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">{t("mediaServers.none")}</p>
      )}
      <ul className="flex flex-col gap-2">
        {servers.data?.map((server) => (
          <li key={server.id} className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{server.name}</span>
              <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
                {kindOf(server.kind)?.label ?? server.kind}
              </span>
              {usesOf(server).map((use) => (
                <span key={use} className="rounded-full border border-accent px-1.5 py-px text-[0.6875rem] text-accent">{use}</span>
              ))}
              <span className="truncate font-mono text-xs text-ink-muted">{server.base_url}</span>
              <span className="ml-auto flex items-center gap-2">
                <Button onClick={() => open(String(server.id))}>{t("mediaServers.edit")}</Button>
                <RemoveButton
                  aria-label={t("mediaServers.removeLabel", { name: server.name })}
                  confirm={{ title: t("mediaServers.removeTitle"), body: t("mediaServers.removeBody", { name: server.name }) }}
                  onClick={() => remove.mutate(server.id)}
                  disabled={remove.isPending}
                />
              </span>
            </div>
            <span className={`text-xs ${server.last_error ? "text-danger" : "text-ink-muted"}`}>
              {server.last_error
                ? server.last_error
                : server.last_ok_at
                  ? t("mediaServers.lastContact", { when: new Date(server.last_ok_at).toLocaleString() })
                  : t("mediaServers.neverContacted")}
            </span>
          </li>
        ))}
      </ul>
      {remove.isError && <p className="text-xs text-danger">{(remove.error as { detail?: string } | null)?.detail ?? t("mediaServers.refused")}</p>}
      {editing === undefined && kinds.data && (
        <div>
          <Button variant="primary" onClick={() => open("new")}>{t("mediaServers.add")}</Button>
        </div>
      )}
      {editing !== undefined && kinds.data && (
        <ServerForm
          key={editing === "new" ? "new" : editing.id}
          kinds={kinds.data}
          server={editing === "new" ? undefined : editing}
          onDone={() => {
            void queryClient.invalidateQueries({ queryKey: SERVERS_KEY });
            open(undefined);
          }}
          onCancel={() => open(undefined)}
        />
      )}
    </div>
  );
}

/** The add and edit form: kind, address and credential, Test, then the library and path mapping and what it is used for. */
function ServerForm({ kinds, server, onDone, onCancel }: { kinds: Kind[]; server?: MediaServer; onDone: () => void; onCancel: () => void }) {
  const categories = useCategories();
  const [kindKey, setKindKey] = useState(server?.kind ?? kinds[0]?.key ?? "");
  const kind = kinds.find((candidate) => candidate.key === kindKey);
  const [name, setName] = useState(server?.name ?? "");
  const [baseUrl, setBaseUrl] = useState(server?.base_url ?? "");
  const [username, setUsername] = useState(server?.username ?? "");
  const [credential, setCredential] = useState("");
  const [uses, setUses] = useState({
    as_metadata: server?.as_metadata ?? false, push_files: server?.push_files ?? false,
    announce: server?.announce ?? false, sync_playback: server?.sync_playback ?? false,
  });
  const [mapping, setMapping] = useState<Record<number, string>>(
    Object.fromEntries((server?.libraries ?? []).map((library) => [library.category_id, library.server_library_id])),
  );
  const [pairs, setPairs] = useState<PathPair[]>(server?.paths ?? []);
  const [tested, setTested] = useState<TestResult | undefined>();
  const [failure, setFailure] = useState<string | undefined>();

  const connection = () => ({
    kind: kindKey, name: name.trim() || (kind?.label ?? kindKey), base_url: baseUrl.trim(), username: username.trim(),
    credential: credential || undefined, enabled: server?.enabled ?? true, ...uses,
  });
  const test = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/media-servers/test", { body: { id: server?.id, connection: connection(), paths: pairs.filter((pair) => pair.solarr_prefix && pair.server_prefix) } })),
    onSuccess: (result) => setTested(result),
    onError: (error) => setFailure((error as { detail?: string }).detail ?? t("mediaServers.refused")),
  });
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async () => {
      const saved = server
        ? await ok(api.PUT("/media-servers/{id}", { params: { path: { id: server.id } }, body: connection() }))
        : await ok(api.POST("/media-servers", { body: connection() }));
      const remote = tested?.libraries ?? [];
      const libraries = Object.entries(mapping)
        .filter(([, remoteId]) => remoteId)
        .map(([categoryId, remoteId]) => ({
          category_id: Number(categoryId), server_library_id: remoteId,
          server_library: remote.find((library) => library.id === remoteId)?.name,
        }));
      await ok(api.PUT("/media-servers/{id}/libraries", { params: { path: { id: saved.id } }, body: libraries }));
      await ok(api.PUT("/media-servers/{id}/paths", {
        params: { path: { id: saved.id } }, body: pairs.filter((pair) => pair.solarr_prefix && pair.server_prefix),
      }));
    },
    onSuccess: onDone,
    onError: (error) => setFailure((error as { detail?: string }).detail ?? t("mediaServers.refused")),
  });

  const fitting = (categories.data ?? []).filter((category) => kind?.media_types.includes(category.media_type));
  const needsCredential = server === undefined && !credential;
  const switches: { key: keyof typeof uses; label: string; offered: boolean | undefined }[] = [
    { key: "as_metadata", label: t("mediaServers.switchMetadata"), offered: kind?.can_metadata },
    { key: "push_files", label: t("mediaServers.switchPush"), offered: kind?.can_push },
    { key: "announce", label: t("mediaServers.switchAnnounce"), offered: kind?.can_announce },
    { key: "sync_playback", label: t("mediaServers.switchPlayback"), offered: kind?.can_playback },
  ];

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4" aria-label={t("mediaServers.formLabel")}>
      <h2 className="text-base font-semibold">{server ? t("mediaServers.editTitle", { name: server.name }) : t("mediaServers.addTitle")}</h2>
      {server === undefined && (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t("mediaServers.kind")}>
          {kinds.map((candidate) => (
            <button
              key={candidate.key}
              type="button"
              role="radio"
              aria-checked={candidate.key === kindKey}
              onClick={() => { setKindKey(candidate.key); setTested(undefined); }}
              className={`flex flex-col gap-0.5 rounded-md border p-2 text-left text-sm ${candidate.key === kindKey ? "border-accent" : "border-border-subtle"}`}
            >
              <span className="font-medium">{candidate.label}</span>
              <span className="text-xs text-ink-muted">{candidate.media_types.join(", ")}</span>
            </button>
          ))}
        </div>
      )}
      <div className="grid gap-2 sm:grid-cols-2">
        <label className={labelStyle}>
          {t("mediaServers.name")}
          <input className={inputStyle} value={name} onChange={(event) => setName(event.target.value)} placeholder={kind?.label} />
        </label>
        <label className={labelStyle}>
          {t("mediaServers.address")}
          <input
            className={inputStyle} value={baseUrl} onChange={(event) => setBaseUrl(event.target.value)}
            placeholder={kind?.default_port_hint ? `http://host:${kind.default_port_hint}` : "http://host:port"}
          />
        </label>
        {(kind?.credential === "password" || kind?.credential === "optional_password") && (
          <label className={labelStyle}>
            {t("mediaServers.username")}
            <input className={inputStyle} value={username} onChange={(event) => setUsername(event.target.value)} autoComplete="off" />
          </label>
        )}
        <label className={labelStyle}>
          {kind?.credential === "key" ? t("mediaServers.key") : t("mediaServers.password")}
          <input
            className={inputStyle} type="password" value={credential} autoComplete="new-password"
            onChange={(event) => setCredential(event.target.value)}
            placeholder={server?.credential.has_value ? t("mediaServers.keepStored") : undefined}
          />
        </label>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Button busy={test.isPending} busyLabel={t("mediaServers.testing")} disabled={!baseUrl.trim() || (needsCredential && kind?.credential !== "optional_password")} onClick={() => { setFailure(undefined); setTested(undefined); test.mutate(); }}>
          {t("mediaServers.test")}
        </Button>
        {tested && tested.ok && (
          <span className="text-sm text-ok">{t("mediaServers.testOk", { name: tested.name ?? "", version: tested.version ?? "" })}</span>
        )}
        {tested && !tested.ok && <span className="text-sm text-danger">{tested.error}</span>}
      </div>
      {tested?.ok && kind?.needs_admin && tested.admin === false && (
        <p className="text-sm text-warn">{t("mediaServers.notAdmin")}</p>
      )}

      {tested?.ok && (
        <>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">{t("mediaServers.librariesTitle")}</legend>
            {fitting.length === 0 && <p className="text-xs text-ink-muted">{t("mediaServers.noFittingLibrary")}</p>}
            {fitting.map((category) => (
              <label key={category.id} className="flex items-center gap-2 text-sm">
                <span className="w-40 truncate">{category.name}</span>
                <select
                  className={inputStyle}
                  value={mapping[category.id] ?? ""}
                  onChange={(event) => setMapping({ ...mapping, [category.id]: event.target.value })}
                >
                  <option value="">{t("mediaServers.notMapped")}</option>
                  {tested.libraries.map((library) => (
                    <option key={library.id} value={library.id}>{library.name}</option>
                  ))}
                </select>
              </label>
            ))}
          </fieldset>
          <fieldset className="flex flex-col gap-2">
            <legend className="text-sm font-medium">{t("mediaServers.pathsTitle")}</legend>
            <p className="text-xs text-ink-muted">{t("mediaServers.pathsHelp")}</p>
            {pairs.map((pair, index) => {
              const check = tested.paths.find((entry) => entry.solarr_prefix === pair.solarr_prefix && entry.server_prefix === pair.server_prefix);
              return (
                <div key={index} className="flex flex-wrap items-center gap-2">
                  <input
                    className={inputStyle} aria-label={t("mediaServers.solarrPath")} value={pair.solarr_prefix} placeholder="/library/music"
                    onChange={(event) => setPairs(pairs.map((entry, at) => (at === index ? { ...entry, solarr_prefix: event.target.value } : entry)))}
                  />
                  <span aria-hidden>{"→"}</span>
                  <input
                    className={inputStyle} aria-label={t("mediaServers.serverPath")} value={pair.server_prefix} placeholder="/data/music"
                    onChange={(event) => setPairs(pairs.map((entry, at) => (at === index ? { ...entry, server_prefix: event.target.value } : entry)))}
                  />
                  {check && (
                    <span className={`text-xs ${check.found ? "text-ok" : "text-warn"}`}>
                      {check.found ? t("mediaServers.pathFound") : t("mediaServers.pathMissing", { path: check.server_prefix })}
                    </span>
                  )}
                  <Button variant="ghost" onClick={() => setPairs(pairs.filter((_, at) => at !== index))}>{t("mediaServers.removePair")}</Button>
                </div>
              );
            })}
            <div>
              <Button onClick={() => setPairs([...pairs, { solarr_prefix: "", server_prefix: "" }])}>{t("mediaServers.addPair")}</Button>
            </div>
          </fieldset>
          <fieldset className="flex flex-col gap-1">
            <legend className="text-sm font-medium">{t("mediaServers.usedFor")}</legend>
            {switches.filter((entry) => entry.offered).map((entry) => (
              <label key={entry.key} className="flex items-center gap-2 text-sm">
                <input type="checkbox" checked={uses[entry.key]} onChange={(event) => setUses({ ...uses, [entry.key]: event.target.checked })} />
                {entry.label}
              </label>
            ))}
          </fieldset>
        </>
      )}
      {failure && <p className="text-sm text-danger">{failure}</p>}
      <div className="flex gap-2">
        <Button variant="primary" busy={save.isPending} disabled={!tested?.ok} onClick={() => { setFailure(undefined); save.mutate(); }}>
          {t("mediaServers.save")}
        </Button>
        <Button variant="ghost" onClick={onCancel}>{t("mediaServers.cancel")}</Button>
      </div>
    </section>
  );
}
