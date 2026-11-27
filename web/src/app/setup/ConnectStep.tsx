import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

import { StepFrame } from "./StepFrame";
import { FolderField, LibraryFields } from "./LibraryFields";
import { Field, Problem, Verdict, inputStyle, primaryButtonStyle, quietButtonStyle } from "./ui";
import {
  DEFAULT_CHOICES,
  libraryOf,
  replaceConnection,
  replaceLibrary,
  type ArrApp,
  type ImportChoices,
  type WizardConnection,
  type WizardState,
} from "./wizardState";

type Test = components["schemas"]["ArrConnectionTest"];
type Found = components["schemas"]["DiscoveredService"];

/** The apps the migrate path connects, with the port each one's own image uses and a library to suggest for it. */
export const CONNECT_APPS: readonly { app: ArrApp; title: string; port: number; mediaType?: string; library?: string }[] = [
  { app: "radarr", title: "Radarr", port: 7878, mediaType: "movie", library: "Movies" },
  { app: "sonarr", title: "Sonarr", port: 8989, mediaType: "tv", library: "TV" },
  { app: "lidarr", title: "Lidarr", port: 8686, mediaType: "music", library: "Music" },
  { app: "readarr", title: "Readarr", port: 8787, mediaType: "book", library: "Books" },
  { app: "whisparr", title: "Whisparr", port: 6969, mediaType: "adult", library: "Adult" },
  { app: "kapowarr", title: "Kapowarr", port: 5656, mediaType: "comic", library: "Comics" },
  { app: "mylar3", title: "Mylar3", port: 8090, mediaType: "comic", library: "Comics" },
  { app: "prowlarr", title: "Prowlarr", port: 9696 },
];

const SIX: readonly ArrApp[] = ["radarr", "sonarr", "lidarr", "readarr", "whisparr", "prowlarr"];

export function appTitle(app: string): string {
  return CONNECT_APPS.find((entry) => entry.app === app)?.title ?? app;
}

/** The plain words for what a failed test means for the person. */
export function problemText(test: Test, title: string, port: number): string {
  switch (test.problem) {
    case "wrong_key":
      return t("setup.test.wrongKey", { app: title });
    case "wrong_port":
      return t("setup.test.wrongPort", { app: title, port });
    case "unreachable":
      return t("setup.test.unreachable");
    default:
      return test.message ?? t("setup.test.other");
  }
}

/** Add, test and remove the apps to move from; each added one carries its own library, folders and import choices. */
export function ConnectStep({ state, update, onBack, onNext }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onNext: () => void }) {
  const [adding, setAdding] = useState<{ app: ArrApp; baseUrl: string } | null>(state.connections.length === 0 ? { app: "radarr", baseUrl: "" } : null);
  return (
    <StepFrame
      title={t("setup.connect.title")}
      description={t("setup.connect.description")}
      onBack={onBack}
      onNext={onNext}
      onSkip={state.connections.length === 0 ? onNext : undefined}
      nextLabel={t("setup.next")}
      canNext={adding === null}
    >
      <Discovery onUse={(found) => setAdding({ app: found.kind as ArrApp, baseUrl: found.base_url })} />
      {state.connections.map((connection) => (
        <ConnectionCard key={connection.instanceId} connection={connection} state={state} update={update} />
      ))}
      {adding ? (
        <ConnectionForm
          key={`${adding.app}|${adding.baseUrl}`}
          initial={adding}
          state={state}
          onCancel={state.connections.length > 0 ? () => setAdding(null) : undefined}
          onAdded={(next) => {
            update(next);
            setAdding(null);
          }}
        />
      ) : (
        <button type="button" onClick={() => setAdding({ app: nextApp(state), baseUrl: "" })} className={quietButtonStyle}>
          {t("setup.connect.addAnother")}
        </button>
      )}
    </StepFrame>
  );
}

function nextApp(state: WizardState): ArrApp {
  return CONNECT_APPS.find((entry) => !state.connections.some((connection) => connection.app === entry.app))?.app ?? "radarr";
}

/** Apps found on their usual addresses, one click from the form; asked when the step opens, and again on request. */
export function useDiscovered() {
  return useQuery({
    queryKey: ["arr-discover"],
    staleTime: Infinity,
    retry: false,
    queryFn: () => ok(api.POST("/arr/discover")),
  });
}

function Discovery({ onUse }: { onUse: (found: Found) => void }) {
  const found = useDiscovered();
  const apps = (found.data ?? []).filter((service) => CONNECT_APPS.some((entry) => entry.app === service.kind));
  return (
    <div className="space-y-2 rounded-md border border-border-subtle bg-canvas p-3 text-xs">
      <div className="flex items-center gap-2">
        <span className="flex-1 text-ink-muted">{found.isFetching ? t("setup.discover.looking") : apps.length > 0 ? t("setup.discover.found") : t("setup.discover.none")}</span>
        <button type="button" onClick={() => void found.refetch()} disabled={found.isFetching} className={quietButtonStyle}>
          {t("setup.discover.again")}
        </button>
      </div>
      {apps.map((service) => (
        <div key={service.kind} className="flex items-center gap-2">
          <span className="flex-1">
            <span className="font-medium text-ink">{appTitle(service.kind)}</span> <span className="font-mono text-ink-muted">{service.base_url}</span>
          </span>
          <button type="button" onClick={() => onUse(service)} className={quietButtonStyle}>
            {t("setup.discover.use")}
          </button>
        </div>
      ))}
    </div>
  );
}

export function ConnectionForm({ initial, state, onAdded, onCancel, only }: {
  initial: { app: ArrApp; baseUrl: string };
  only?: readonly ArrApp[];
  state: WizardState;
  onAdded: (next: WizardState) => void;
  onCancel?: () => void;
}) {
  const queryClient = useQueryClient();
  const [app, setApp] = useState<ArrApp>(initial.app);
  const entry = CONNECT_APPS.find((candidate) => candidate.app === app) ?? CONNECT_APPS[0]!;
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState(initial.baseUrl);
  const [apiKey, setApiKey] = useState("");
  const [tested, setTested] = useState<{ signature: string; result: Test } | null>(null);
  const signature = `${app}|${baseUrl.trim()}|${apiKey.trim()}`;
  const connections = useQuery({ queryKey: ["arr-instances"], queryFn: () => ok(api.GET("/arr/instances")) });
  const duplicate = (connections.data ?? []).find((existing) => existing.base_url.replace(/\/+$/, "") === baseUrl.trim().replace(/\/+$/, ""));

  const test = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/arr/test", { body: { app, base_url: baseUrl.trim(), api_key: apiKey.trim() } })),
    onSuccess: (result) => setTested({ signature, result }),
  });
  const verdict = tested?.signature === signature ? tested.result : undefined;

  const add = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async (): Promise<WizardState> => {
      const shown = name.trim() || entry.title;
      const instance = await ok(api.POST("/arr/instances", { body: { app, name: shown, base_url: baseUrl.trim(), api_key: apiKey.trim(), enabled: true, push_monitoring: false } }));
      const connection: WizardConnection = {
        instanceId: instance.id, app, name: shown, baseUrl: baseUrl.trim(), version: verdict?.version,
        folderMaps: [], choices: { ...DEFAULT_CHOICES }, push: false,
      };
      if (!entry.mediaType) return { ...state, connections: [...state.connections, connection] };
      const roots = await api.GET("/arr/instances/{id}/rootfolders", { params: { path: { id: instance.id } } });
      connection.folderMaps = (roots.data ?? []).map((root) => ({ arrPath: root.path, here: root.path }));
      connection.libraryKey = `arr-${instance.id}`;
      return {
        ...state,
        connections: [...state.connections, connection],
        libraries: [
          ...state.libraries,
          { key: connection.libraryKey, name: entry.library ?? shown, mediaType: entry.mediaType, kind: "", folders: [], starter: "arr" },
        ],
      };
    },
    onSuccess: async (next) => {
      await queryClient.invalidateQueries({ queryKey: ["arr-instances"] });
      onAdded(next);
    },
  });

  return (
    <form
      className="space-y-3 rounded-md border border-border-subtle bg-canvas p-3"
      onSubmit={(event) => {
        event.preventDefault();
        add.mutate();
      }}
    >
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("setup.connect.app")}>
          <select className={inputStyle} value={app} onChange={(event) => setApp(event.target.value as ArrApp)}>
            {CONNECT_APPS.filter((candidate) => !only || only.includes(candidate.app)).map((candidate) => (
              <option key={candidate.app} value={candidate.app}>
                {candidate.title}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("setup.connect.name")}>
          <input className={inputStyle} value={name} placeholder={entry.title} autoComplete="off" onChange={(event) => setName(event.target.value)} />
        </Field>
      </div>
      <Field label={t("setup.connect.address")} hint={t("setup.connect.addressHint", { app: entry.title })}>
        <input className={inputStyle} value={baseUrl} placeholder={`http://${app}:${entry.port}`} autoComplete="off" onChange={(event) => setBaseUrl(event.target.value)} />
      </Field>
      <Field label={t("setup.connect.key")} hint={SIX.includes(app) ? t("setup.connect.keyHint", { app: entry.title }) : t("setup.connect.keyHintOther", { app: entry.title })}>
        <input className={inputStyle} type="password" value={apiKey} autoComplete="off" onChange={(event) => setApiKey(event.target.value)} />
      </Field>
      {duplicate && <Verdict ok={false}>{t("setup.connect.duplicate", { name: duplicate.name })}</Verdict>}
      {verdict?.ok && <Verdict ok>{t("setup.test.ok", { app: verdict.app_name ?? entry.title, version: verdict.version ?? "" })}</Verdict>}
      {verdict && !verdict.ok && <Verdict ok={false}>{problemText(verdict, entry.title, entry.port)}</Verdict>}
      {test.isError && <Problem error={test.error} />}
      {add.isError && <Problem error={add.error} />}
      <div className="flex gap-2">
        <button type="button" disabled={!baseUrl.trim() || !apiKey.trim() || test.isPending} onClick={() => test.mutate()} className={quietButtonStyle}>
          {test.isPending ? t("setup.test.testing") : t("setup.test.button")}
        </button>
        <button type="submit" disabled={!verdict?.ok || add.isPending || duplicate !== undefined} className={primaryButtonStyle}>
          {add.isPending ? t("setup.connect.adding") : t("setup.connect.add")}
        </button>
        {onCancel && (
          <button type="button" onClick={onCancel} className={quietButtonStyle}>
            {t("setup.cancel")}
          </button>
        )}
      </div>
    </form>
  );
}

function ConnectionCard({ connection, state, update }: { connection: WizardConnection; state: WizardState; update: (next: WizardState) => void }) {
  const queryClient = useQueryClient();
  const library = libraryOf(state, connection.libraryKey);
  const change = (patch: Partial<WizardConnection>) => update(replaceConnection(state, connection.instanceId, patch));
  const choose = (patch: Partial<ImportChoices>) => change({ choices: { ...connection.choices, ...patch } });
  const remove = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.DELETE("/arr/instances/{id}", { params: { path: { id: connection.instanceId } } })),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["arr-instances"] });
      update({
        ...state,
        connections: state.connections.filter((one) => one.instanceId !== connection.instanceId),
        libraries: state.libraries.filter((one) => one.key !== connection.libraryKey),
      });
    },
  });
  const checkbox = (label: string, checked: boolean, on: (value: boolean) => void, title?: string) => (
    <label className="flex items-center gap-1.5 text-xs" title={title}>
      <input type="checkbox" checked={checked} onChange={(event) => on(event.target.checked)} />
      {label}
    </label>
  );
  return (
    <section className="space-y-3 rounded-md border border-border-subtle p-3">
      <div className="flex items-center gap-2">
        <h3 className="flex-1 text-sm font-medium">
          <span aria-hidden className="text-ok">{"✓ "}</span>
          {connection.name} <span className="font-normal text-ink-muted">{appTitle(connection.app)}{connection.version ? ` ${connection.version}` : ""}</span>
        </h3>
        <button type="button" disabled={remove.isPending} onClick={() => remove.mutate()} className={quietButtonStyle}>
          {t("setup.remove")}
        </button>
      </div>
      {remove.isError && <Problem error={remove.error} />}
      {connection.app === "prowlarr" ? (
        <p className="text-xs text-ink-muted">{t("setup.connect.prowlarrNote")}</p>
      ) : (
        <>
          {library && <LibraryFields library={library} onChange={(patch) => update(replaceLibrary(state, library.key, patch))} />}
          <div className="space-y-2">
            <p className="text-xs font-medium text-ink-muted">{t("setup.connect.folders", { app: appTitle(connection.app) })}</p>
            <p className="text-xs text-ink-muted">{t("setup.connect.foldersHelp")}</p>
            {connection.folderMaps.length === 0 && <p className="text-xs text-warn">{t("setup.connect.noFolders")}</p>}
            {connection.folderMaps.map((map, index) => (
              <div key={map.arrPath} className="space-y-1">
                <p className="text-xs text-ink-muted">
                  {t("setup.connect.folderIn", { app: appTitle(connection.app) })} <span className="font-mono text-ink">{map.arrPath}</span>
                </p>
                <FolderField
                  value={map.here}
                  label={t("setup.connect.folderHere")}
                  onChange={(here) => change({ folderMaps: connection.folderMaps.map((one, at) => (at === index ? { ...one, here } : one)) })}
                />
              </div>
            ))}
          </div>
          <fieldset className="space-y-1">
            <legend className="mb-1 text-xs font-medium text-ink-muted">{t("setup.connect.bringIn")}</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-1">
              {checkbox(t("setup.choice.monitored"), connection.choices.monitored, (monitored) => choose({ monitored }), t("setup.choice.monitoredHelp"))}
              {checkbox(t("setup.choice.unmonitored"), connection.choices.unmonitored, (unmonitored) => choose({ unmonitored }), t("setup.choice.unmonitoredHelp"))}
              {checkbox(t("setup.choice.indexers"), connection.choices.indexers, (indexers) => choose({ indexers }), t("setup.choice.indexersHelp"))}
              {checkbox(t("setup.choice.clients"), connection.choices.downloadClients, (downloadClients) => choose({ downloadClients }), t("setup.choice.clientsHelp"))}
              {checkbox(t("setup.choice.lists"), connection.choices.importLists, (importLists) => choose({ importLists }), t("setup.choice.listsHelp"))}
              {checkbox(t("setup.choice.push", { app: appTitle(connection.app) }), connection.push, (push) => change({ push }), t("setup.choice.pushHelp"))}
            </div>
          </fieldset>
        </>
      )}
    </section>
  );
}
