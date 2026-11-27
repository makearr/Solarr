import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { kindSecret } from "@/lib/downloaderKinds";
import { t } from "@/lib/i18n";
import { useDownloaderKinds } from "@/lib/resources";

import { ArrRunStatus } from "../ArrImport";
import { buildTasks, runTasks, type TaskStatus } from "./runSetup";
import { Panel, Problem, Verdict, inputStyle, primaryButtonStyle, quietButtonStyle } from "./ui";
import type { WizardState } from "./wizardState";

type Indexer = components["schemas"]["Indexer"];
type Downloader = components["schemas"]["Downloader"];

/** Does the work, then shows how each import is going and what is left for the person: keys, client tests, health warnings. */
export function FinishStep({ state, update, onDone }: { state: WizardState; update: (next: WizardState) => void; onDone: () => void }) {
  const queryClient = useQueryClient();
  const [statuses, setStatuses] = useState<Record<string, TaskStatus>>({});
  const [running, setRunning] = useState(false);
  const started = useRef(false);
  const latest = useRef(state);
  latest.current = state;
  const tasks = buildTasks(state);

  const run = async () => {
    setRunning(true);
    await runTasks(
      latest.current,
      (next) => {
        latest.current = next;
        update(next);
      },
      (id, status) => setStatuses((before) => ({ ...before, [id]: status })),
    );
    setRunning(false);
    await Promise.all(["libraries", "categories", "rootfolders", "indexers", "downloaders", "health", "arr-instances"].map((key) => queryClient.invalidateQueries({ queryKey: [key] })));
  };
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    void run();
    // Once on arrival: a retry is the button.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const failed = tasks.some((task) => statuses[task.id]?.state === "failed");
  const leave = () => {
    update({ ...state, finished: true });
    onDone();
  };
  return (
    <div className="space-y-4">
      <Panel title={running ? t("setup.finish.working") : failed ? t("setup.finish.someFailed") : t("setup.finish.done")} description={t("setup.finish.description")}>
        <ul className="space-y-1 text-sm">
          {tasks.map((task) => {
            const status = statuses[task.id]?.state ?? "waiting";
            return (
              <li key={task.id} className="flex flex-col">
                <span className={status === "failed" ? "text-danger" : status === "done" ? "text-ink" : "text-ink-muted"}>
                  <span aria-hidden>{status === "done" ? "✓ " : status === "failed" ? "✗ " : status === "running" ? "… " : "· "}</span>
                  {task.label}
                  {status === "skipped" ? ` (${t("setup.finish.skipped")})` : ""}
                </span>
                {statuses[task.id]?.message && <span className="pl-4 text-xs text-danger">{statuses[task.id]!.message}</span>}
              </li>
            );
          })}
        </ul>
        {tasks.length === 0 && <p className="text-xs text-ink-muted">{t("setup.finish.nothing")}</p>}
        {failed && !running && (
          <button type="button" onClick={() => void run()} className={quietButtonStyle}>
            {t("setup.finish.retry")}
          </button>
        )}
      </Panel>

      {state.connections.some((connection) => connection.runId !== undefined) && (
        <Panel title={t("setup.finish.imports")} description={t("setup.finish.importsHelp")}>
          {state.connections.filter((connection) => connection.runId !== undefined).map((connection) => (
            <div key={connection.instanceId} className="space-y-1 text-xs">
              <p className="font-medium text-ink">{t("setup.importOf", { name: connection.name })}</p>
              <ArrRunStatus runId={connection.runId!} />
            </div>
          ))}
        </Panel>
      )}

      {!running && (
        <Panel title={t("setup.left.title")} description={t("setup.left.description")}>
          <KeylessIndexers />
          <ClientsToFinish category={`solarr-${state.libraries[0]?.mediaType ?? "media"}`} />
          <HealthLines />
        </Panel>
      )}

      <div className="flex justify-end">
        <button type="button" disabled={running} onClick={leave} className={primaryButtonStyle}>
          {t("setup.finish.go")}
        </button>
      </div>
    </div>
  );
}

function KeylessIndexers() {
  const indexers = useQuery({ queryKey: ["indexers"], queryFn: () => ok(api.GET("/indexers")) });
  const keyless = (indexers.data ?? []).filter((indexer) => !indexer.api_key.has_value && !indexer.enabled && indexer.impl !== "public");
  if (keyless.length === 0) return <Verdict ok>{t("setup.left.keysDone")}</Verdict>;
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-medium">{t("setup.left.keys", { count: keyless.length })}</h3>
      <p className="text-xs text-ink-muted">{t("setup.left.keysHelp")}</p>
      {keyless.map((indexer) => (
        <KeyRow key={indexer.id} indexer={indexer} />
      ))}
    </section>
  );
}

function KeyRow({ indexer }: { indexer: Indexer }) {
  const queryClient = useQueryClient();
  const [key, setKey] = useState("");
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async () => {
      await ok(api.POST("/indexers/{id}/apikey", { params: { path: { id: indexer.id } }, body: { api_key: key.trim() } }));
      await ok(api.PATCH("/indexers/{id}", {
        params: { path: { id: indexer.id } },
        body: { name: indexer.name, impl: indexer.impl, base_url: indexer.base_url, enabled: true, priority: indexer.priority },
      }));
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["indexers"] }),
  });
  return (
    <div className="space-y-1">
      <div className="flex items-center gap-2">
        <span className="w-40 shrink-0 truncate text-xs" title={indexer.base_url}>{indexer.name}</span>
        <input className={inputStyle} type="password" aria-label={t("setup.left.keyFor", { name: indexer.name })} value={key} autoComplete="off" onChange={(event) => setKey(event.target.value)} />
        <button type="button" disabled={!key.trim() || save.isPending} onClick={() => save.mutate()} className={quietButtonStyle}>
          {t("setup.left.saveKey")}
        </button>
      </div>
      {save.isError && <Problem error={save.error} />}
    </div>
  );
}

function ClientsToFinish({ category }: { category: string }) {
  const downloaders = useQuery({ queryKey: ["downloaders"], queryFn: () => ok(api.GET("/downloaders")) });
  const waiting = (downloaders.data ?? []).filter((downloader) => !downloader.enabled);
  if (waiting.length === 0) return <Verdict ok>{t("setup.left.clientsDone")}</Verdict>;
  return (
    <section className="space-y-3">
      <h3 className="text-sm font-medium">{t("setup.left.clients", { count: waiting.length })}</h3>
      <p className="text-xs text-ink-muted">{t("setup.left.clientsHelp")}</p>
      {waiting.map((downloader) => (
        <ClientRow key={downloader.id} downloader={downloader} category={category} />
      ))}
    </section>
  );
}

function ClientRow({ downloader, category }: { downloader: Downloader; category: string }) {
  const queryClient = useQueryClient();
  const kind = useDownloaderKinds().data?.find((one) => one.impl === downloader.impl);
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  const [chosen, setCategory] = useState(downloader.options?.category || category);
  const finish = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async () => {
      const credentials = kindSecret(kind, username, secret);
      if (credentials) await ok(api.POST("/downloaders/{id}/credentials", { params: { path: { id: downloader.id } }, body: { credentials } }));
      const body = { name: downloader.name, impl: downloader.impl, base_url: downloader.base_url, protocol: downloader.protocol, options: { ...downloader.options, category: chosen || undefined } };
      await ok(api.PATCH("/downloaders/{id}", { params: { path: { id: downloader.id } }, body: { ...body, enabled: false } }));
      const result = await ok(api.POST("/downloaders/{id}/test", { params: { path: { id: downloader.id } } }));
      if (result.ok) await ok(api.PATCH("/downloaders/{id}", { params: { path: { id: downloader.id } }, body: { ...body, enabled: true } }));
      return result;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["downloaders"] }),
  });
  return (
    <div className="space-y-2 rounded-md border border-border-subtle p-3">
      <p className="text-sm">
        {downloader.name} <span className="font-mono text-xs text-ink-muted">{downloader.base_url}</span>
      </p>
      <div className="grid gap-2 sm:grid-cols-3">
        {kind?.user_name && (
          <input className={inputStyle} aria-label={t("setup.clients.username")} placeholder={t("setup.clients.username")} value={username} autoComplete="off" onChange={(event) => setUsername(event.target.value)} />
        )}
        {kind?.secret_label && (
          <input className={inputStyle} type="password" aria-label={kind.secret_label} placeholder={kind.secret_label} value={secret} autoComplete="off" onChange={(event) => setSecret(event.target.value)} />
        )}
        <input className={inputStyle} aria-label={t("setup.clients.category")} placeholder={t("setup.clients.category")} value={chosen} autoComplete="off" onChange={(event) => setCategory(event.target.value)} />
      </div>
      <p className="text-xs text-ink-muted">{t("setup.clients.categoryHint")}</p>
      {finish.data && <Verdict ok={finish.data.ok}>{finish.data.ok ? t("setup.clients.switchedOn", { message: finish.data.message }) : finish.data.message}</Verdict>}
      {finish.isError && <Problem error={finish.error} />}
      <button type="button" disabled={finish.isPending} onClick={() => finish.mutate()} className={quietButtonStyle}>
        {finish.isPending ? t("setup.test.testing") : t("setup.clients.testAndOn")}
      </button>
    </div>
  );
}

function HealthLines() {
  const health = useQuery({ queryKey: ["health"], queryFn: () => ok(api.GET("/system/health")) });
  const lines = health.data ?? [];
  if (lines.length === 0) return <Verdict ok>{t("setup.left.healthDone")}</Verdict>;
  return (
    <section className="space-y-1">
      <h3 className="text-sm font-medium">{t("setup.left.health")}</h3>
      {lines.map((line) => (
        <Verdict key={line.id} ok={false}>{line.message}</Verdict>
      ))}
    </section>
  );
}

