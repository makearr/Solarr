import { useState } from "react";
import { PageHeader } from "@/ui/PageHeader";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";

import { API_PREFIX, api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, tOr } from "@/lib/i18n";
import { useCategories } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { RemoveButton } from "@/ui/RemoveButton";
import { inputStyle, labelStyle } from "@/ui/form";

type Webhook = components["schemas"]["Webhook"];
type Inbound = components["schemas"]["InboundWebhook"];
type Action = Inbound["actions"][number];

const OUT_KEY = ["webhooks"];
const IN_KEY = ["incoming-webhooks"];
const ACTIONS: Action[] = ["scan", "import", "refresh", "playback", "search"];
const DEFAULT_ACTIONS: Action[] = ["scan", "import", "refresh", "playback"];

type OutgoingForm = { name: string; preset: string; address: string; events: string[]; adding: boolean };

/** What stops an outgoing webhook from being saved, as message keys; an address is only needed when adding. */
export function outgoingProblems(form: OutgoingForm): string[] {
  const problems: string[] = [];
  if (!form.name.trim()) problems.push("webhooks.needName");
  if (!form.preset) problems.push("webhooks.needPreset");
  if (form.adding || form.address.trim()) {
    try {
      const parsed = new URL(form.address.trim());
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") problems.push("webhooks.needAddress");
    } catch {
      problems.push("webhooks.needAddress");
    }
  }
  if (form.events.length === 0) problems.push("webhooks.needEvent");
  return problems;
}

/** The curl line that makes one action, for the sender that is a shell script. */
export function curlLine(origin: string, id: number, token: string, action: Action): string {
  const body: Record<string, string | number> = { action };
  if (action === "scan") body.library_id = 1;
  if (action === "import") body.path = "/downloads/finished/Some.Release";
  if (action === "refresh" || action === "search") body.work_id = 1;
  return `curl -X POST -H 'Authorization: Bearer ${token}' -H 'Content-Type: application/json' -d '${JSON.stringify(body)}' ${origin}${API_PREFIX}/hooks/${id}`;
}

/** Connections > Webhooks: what Solarr tells other services, and what other tools may ask of Solarr. */
export function Webhooks() {
  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-8 overflow-y-auto p-6">
      <Outgoing />
      <Incoming />
    </div>
  );
}

function Outgoing() {
  const navigate = useNavigate();
  const { out } = useSearch({ strict: false }) as { out?: string };
  const queryClient = useQueryClient();
  const hooks = useQuery({ queryKey: OUT_KEY, queryFn: () => ok(api.GET("/webhooks")) });
  const vocabulary = useQuery({ queryKey: ["webhooks", "events"], queryFn: () => ok(api.GET("/webhooks/events")) });
  const remove = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (id: number) => ok(api.DELETE("/webhooks/{id}", { params: { path: { id } } })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: OUT_KEY }),
  });
  const open = (value: string | undefined) => void navigate({ to: "/connections/webhooks", search: (old) => ({ ...old, out: value }) as never });
  const editing = out === "new" ? "new" : hooks.data?.find((hook) => String(hook.id) === out);

  return (
    <section className="flex flex-col gap-3" aria-label={t("webhooks.outTitle")}>
      <header className="flex flex-col gap-1">
        <PageHeader title={t("webhooks.title")} />
        <h2 className="text-base font-medium">{t("webhooks.outTitle")}</h2>
        <p className="text-sm text-ink-muted">{t("webhooks.outIntro")}</p>
      </header>
      {hooks.isError && <p className="text-sm text-danger">{t("webhooks.couldNotRead")}</p>}
      {hooks.data && hooks.data.length === 0 && editing === undefined && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">{t("webhooks.outNone")}</p>
      )}
      <ul className="flex flex-col gap-2">
        {hooks.data?.map((hook) => (
          <li key={hook.id} className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{hook.name}</span>
              <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">{hook.preset}</span>
              {!hook.enabled && <span className="rounded-full border border-warn px-1.5 py-px text-[0.6875rem] text-warn">{t("webhooks.off")}</span>}
              <span className="truncate font-mono text-xs text-ink-muted">{hook.address_hint}</span>
              <span className="ml-auto flex items-center gap-2">
                <Button onClick={() => open(String(hook.id))}>{t("webhooks.edit")}</Button>
                <RemoveButton
                  aria-label={t("webhooks.removeLabel", { name: hook.name })}
                  confirm={{ title: t("webhooks.removeTitle"), body: t("webhooks.removeBody", { name: hook.name }) }}
                  onClick={() => remove.mutate(hook.id)}
                  disabled={remove.isPending}
                />
              </span>
            </div>
            <span className={`text-xs ${hook.last_error ? "text-danger" : "text-ink-muted"}`}>{lastDelivery(hook)}</span>
          </li>
        ))}
      </ul>
      {editing === undefined && vocabulary.data && (
        <div>
          <Button variant="primary" onClick={() => open("new")}>{t("webhooks.outAdd")}</Button>
        </div>
      )}
      {editing !== undefined && vocabulary.data && (
        <OutgoingForm
          key={editing === "new" ? "new" : editing.id}
          vocabulary={vocabulary.data}
          hook={editing === "new" ? undefined : editing}
          onDone={() => {
            void queryClient.invalidateQueries({ queryKey: OUT_KEY });
            open(undefined);
          }}
          onCancel={() => open(undefined)}
        />
      )}
    </section>
  );
}

function lastDelivery(hook: Webhook): string {
  const waiting = hook.pending > 0 ? ` ${t("webhooks.pending", { count: hook.pending })}` : "";
  if (hook.last_error) return `${hook.last_error}${waiting}`;
  if (hook.last_at) return `${t("webhooks.lastStatus", { status: hook.last_status ?? 0, when: new Date(hook.last_at).toLocaleString() })}${waiting}`;
  return `${t("webhooks.neverSent")}${waiting}`;
}

function toggled<T>(list: T[], value: T): T[] {
  return list.includes(value) ? list.filter((item) => item !== value) : [...list, value];
}

function OutgoingForm({ vocabulary, hook, onDone, onCancel }: {
  vocabulary: components["schemas"]["WebhookVocabulary"]; hook?: Webhook; onDone: () => void; onCancel: () => void;
}) {
  const categories = useCategories();
  const [name, setName] = useState(hook?.name ?? "");
  const [preset, setPreset] = useState(hook?.preset ?? vocabulary.presets[0]?.key ?? "");
  const [address, setAddress] = useState("");
  const [secret, setSecret] = useState("");
  const [events, setEvents] = useState<string[]>(hook?.events ?? []);
  const [libraries, setLibraries] = useState<number[]>(hook?.category_ids ?? []);
  const [includePaths, setIncludePaths] = useState(hook?.include_paths ?? false);
  const [includeAdult, setIncludeAdult] = useState(hook?.include_adult ?? false);
  const [enabled, setEnabled] = useState(hook?.enabled ?? true);
  const [failure, setFailure] = useState<string | undefined>();
  const [tested, setTested] = useState<components["schemas"]["WebhookTestResult"] | undefined>();
  const problems = outgoingProblems({ name, preset, address, events, adding: hook === undefined });
  const testable = events.filter((event) => event !== "test");
  const groups = [...new Set(vocabulary.events.filter((event) => event.key !== "test").map((event) => event.group))];

  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => {
      const body = {
        name: name.trim(), preset, address: address.trim() || undefined, secret: secret || undefined, events: testable,
        category_ids: libraries, include_paths: includePaths, include_adult: includeAdult, enabled,
      };
      return hook
        ? ok(api.PUT("/webhooks/{id}", { params: { path: { id: hook.id } }, body }))
        : ok(api.POST("/webhooks", { body }));
    },
    onSuccess: onDone,
    onError: (error) => setFailure((error as { detail?: string }).detail ?? t("webhooks.refused")),
  });
  const test = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/webhooks/{id}/test", { params: { path: { id: hook?.id ?? 0 } } })),
    onSuccess: setTested,
    onError: (error) => setTested({ ok: false, status: 0, reply: "", error: (error as { detail?: string }).detail ?? t("webhooks.refused") }),
  });

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4" aria-label={t("webhooks.outFormLabel")}>
      <h3 className="text-base font-semibold">{hook ? t("webhooks.editTitle", { name: hook.name }) : t("webhooks.outAddTitle")}</h3>
      <div className="grid gap-2 sm:grid-cols-2">
        <label className={labelStyle}>
          {t("webhooks.name")}
          <input className={inputStyle} value={name} onChange={(event) => setName(event.target.value)} />
        </label>
        <label className={labelStyle}>
          {t("webhooks.preset")}
          <select className={inputStyle} value={preset} onChange={(event) => setPreset(event.target.value)}>
            {vocabulary.presets.map((candidate) => (
              <option key={candidate.key} value={candidate.key}>{candidate.name}</option>
            ))}
          </select>
          <span className="text-ink-muted">{vocabulary.presets.find((candidate) => candidate.key === preset)?.description}</span>
        </label>
        <label className={labelStyle}>
          {t("webhooks.address")}
          <input
            className={inputStyle} type="password" autoComplete="off" value={address} onChange={(event) => setAddress(event.target.value)}
            placeholder={hook ? t("webhooks.keepStored", { host: hook.address_hint }) : "https://"}
          />
        </label>
        <label className={labelStyle}>
          {t("webhooks.secret")}
          <input
            className={inputStyle} type="password" autoComplete="new-password" value={secret} onChange={(event) => setSecret(event.target.value)}
            placeholder={hook?.has_secret ? t("webhooks.keepSecret") : undefined}
          />
        </label>
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="text-sm font-medium">{t("webhooks.events")}</legend>
        {groups.map((group) => (
          <div key={group} className="flex flex-col gap-1">
            <span className="text-xs font-medium text-ink-muted">{tOr(`webhooks.group.${group}`, group)}</span>
            <div className="grid gap-1 sm:grid-cols-2">
              {vocabulary.events.filter((event) => event.group === group && event.key !== "test").map((event) => (
                <label key={event.key} className="flex items-start gap-2 text-sm">
                  <input type="checkbox" checked={events.includes(event.key)} onChange={() => setEvents(toggled(events, event.key))} />
                  <span className="flex flex-col">
                    <span>{tOr(`webhooks.event.${event.key}`, event.key)}</span>
                  </span>
                </label>
              ))}
            </div>
          </div>
        ))}
      </fieldset>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm font-medium">{t("webhooks.libraries")}</legend>
        <p className="text-xs text-ink-muted">{t("webhooks.librariesNote")}</p>
        <div className="grid gap-1 sm:grid-cols-2">
          {(categories.data ?? []).map((category) => (
            <label key={category.id} className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={libraries.includes(category.id)} onChange={() => setLibraries(toggled(libraries, category.id))} />
              {category.name}
            </label>
          ))}
        </div>
      </fieldset>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={includePaths} onChange={(event) => setIncludePaths(event.target.checked)} />
        {t("webhooks.includePaths")}
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={includeAdult} onChange={(event) => setIncludeAdult(event.target.checked)} />
        {t("webhooks.includeAdult")}
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
        {t("webhooks.enabled")}
      </label>
      <details className="text-xs text-ink-muted">
        <summary className="cursor-pointer">{t("webhooks.whatIsSent")}</summary>
        <p className="py-1">{t("webhooks.whatIsSentNote")}</p>
        <pre className="overflow-x-auto rounded-md border border-border-subtle bg-canvas p-2 font-mono text-[0.6875rem]">{vocabulary.presets.find((candidate) => candidate.key === preset)?.sample || SAMPLE_PAYLOAD}</pre>
      </details>
      {problems.length > 0 && <ul className="text-xs text-ink-muted">{problems.map((key) => <li key={key}>{tOr(key, key)}</li>)}</ul>}
      {failure && <p className="text-sm text-danger">{failure}</p>}
      {tested && (
        <p className={`text-sm ${tested.ok ? "text-ok" : "text-danger"}`}>
          {tested.ok ? t("webhooks.testOk", { status: tested.status }) : (tested.error ?? t("webhooks.testFailed", { status: tested.status }))}
          {tested.reply ? ` ${tested.reply}` : ""}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        <Button variant="primary" busy={save.isPending} disabled={problems.length > 0} onClick={() => { setFailure(undefined); save.mutate(); }}>
          {t("webhooks.save")}
        </Button>
        {hook && (
          <Button busy={test.isPending} busyLabel={t("webhooks.testing")} onClick={() => { setTested(undefined); test.mutate(); }}>
            {t("webhooks.test")}
          </Button>
        )}
        <Button variant="ghost" onClick={onCancel}>{t("webhooks.cancel")}</Button>
      </div>
    </section>
  );
}

const SAMPLE_PAYLOAD = `{
  "schema": 1, "event": "imported", "at": 1767225600, "delivery": 12,
  "instance": {"name": "Solarr", "version": "..."},
  "library": {"id": 2, "name": "Comics", "media_type": "comic", "kind": "comic"},
  "work": {"id": 41, "title": "Example Series", "year": 2024},
  "files": [{"name": "Example Series 001.cbz", "size": 52428800}]
}`;

function Incoming() {
  const navigate = useNavigate();
  const { inn } = useSearch({ strict: false }) as { inn?: string };
  const queryClient = useQueryClient();
  const hooks = useQuery({ queryKey: IN_KEY, queryFn: () => ok(api.GET("/incoming-webhooks")) });
  const [issued, setIssued] = useState<{ webhook: Inbound; token: string } | undefined>();
  const remove = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (id: number) => ok(api.DELETE("/incoming-webhooks/{id}", { params: { path: { id } } })),
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: IN_KEY }),
  });
  const rotate = useMutation({
    mutationFn: (id: number) => ok(api.POST("/incoming-webhooks/{id}/token", { params: { path: { id } } })),
    onSuccess: (created) => setIssued(created),
  });
  const open = (value: string | undefined) => void navigate({ to: "/connections/webhooks", search: (old) => ({ ...old, inn: value }) as never });
  const editing = inn === "new" ? "new" : hooks.data?.find((hook) => String(hook.id) === inn);

  return (
    <section className="flex flex-col gap-3" aria-label={t("webhooks.inTitle")}>
      <header className="flex flex-col gap-1">
        <h2 className="text-base font-medium">{t("webhooks.inTitle")}</h2>
        <p className="text-sm text-ink-muted">{t("webhooks.inIntro")}</p>
      </header>
      {issued && <TokenPanel issued={issued} onClose={() => setIssued(undefined)} />}
      {hooks.data && hooks.data.length === 0 && editing === undefined && (
        <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">{t("webhooks.inNone")}</p>
      )}
      <ul className="flex flex-col gap-2">
        {hooks.data?.map((hook) => (
          <li key={hook.id} className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{hook.name}</span>
              {hook.actions.map((action) => (
                <span key={action} className="rounded-full border border-accent px-1.5 py-px text-[0.6875rem] text-accent">{t(`webhooks.action.${action}`)}</span>
              ))}
              {!hook.enabled && <span className="rounded-full border border-warn px-1.5 py-px text-[0.6875rem] text-warn">{t("webhooks.off")}</span>}
              <span className="ml-auto flex items-center gap-2">
                <Button onClick={() => open(String(hook.id))}>{t("webhooks.edit")}</Button>
                <Button onClick={() => rotate.mutate(hook.id)}>{t("webhooks.newToken")}</Button>
                <RemoveButton
                  aria-label={t("webhooks.removeLabel", { name: hook.name })}
                  confirm={{ title: t("webhooks.removeTitle"), body: t("webhooks.removeInBody", { name: hook.name }) }}
                  onClick={() => remove.mutate(hook.id)}
                  disabled={remove.isPending}
                />
              </span>
            </div>
            <span className="text-xs text-ink-muted">
              {hook.last_used_at ? t("webhooks.lastUsed", { when: new Date(hook.last_used_at).toLocaleString() }) : t("webhooks.neverUsed")}
            </span>
          </li>
        ))}
      </ul>
      {editing === undefined && (
        <div>
          <Button variant="primary" onClick={() => open("new")}>{t("webhooks.inAdd")}</Button>
        </div>
      )}
      {editing !== undefined && (
        <IncomingForm
          key={editing === "new" ? "new" : editing.id}
          hook={editing === "new" ? undefined : editing}
          onDone={(created) => {
            void queryClient.invalidateQueries({ queryKey: IN_KEY });
            if (created) setIssued(created);
            open(undefined);
          }}
          onCancel={() => open(undefined)}
        />
      )}
    </section>
  );
}

function IncomingForm({ hook, onDone, onCancel }: { hook?: Inbound; onDone: (created?: { webhook: Inbound; token: string }) => void; onCancel: () => void }) {
  const categories = useCategories();
  const servers = useQuery({ queryKey: ["media-servers"], queryFn: () => ok(api.GET("/media-servers")) });
  const [name, setName] = useState(hook?.name ?? "");
  const [actions, setActions] = useState<Action[]>(hook?.actions ?? DEFAULT_ACTIONS);
  const [libraries, setLibraries] = useState<number[]>(hook?.category_ids ?? []);
  const [serverId, setServerId] = useState(hook?.media_server_id ?? 0);
  const [enabled, setEnabled] = useState(hook?.enabled ?? true);
  const [failure, setFailure] = useState<string | undefined>();
  const syncing = (servers.data ?? []).filter((server) => server.sync_playback);
  const save = useMutation({
    meta: { handlesErrors: true },
    mutationFn: async () => {
      const body = { name: name.trim(), actions, category_ids: libraries, media_server_id: serverId, enabled };
      if (hook) {
        await ok(api.PUT("/incoming-webhooks/{id}", { params: { path: { id: hook.id } }, body }));
        return undefined;
      }
      return ok(api.POST("/incoming-webhooks", { body }));
    },
    onSuccess: onDone,
    onError: (error) => setFailure((error as { detail?: string }).detail ?? t("webhooks.refused")),
  });

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4" aria-label={t("webhooks.inFormLabel")}>
      <h3 className="text-base font-semibold">{hook ? t("webhooks.editTitle", { name: hook.name }) : t("webhooks.inAddTitle")}</h3>
      <label className={labelStyle}>
        {t("webhooks.name")}
        <input className={inputStyle} value={name} onChange={(event) => setName(event.target.value)} />
      </label>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm font-medium">{t("webhooks.actions")}</legend>
        {ACTIONS.map((action) => (
          <label key={action} className="flex items-start gap-2 text-sm">
            <input type="checkbox" checked={actions.includes(action)} onChange={() => setActions(toggled(actions, action))} />
            <span className="flex flex-col">
              <span>{t(`webhooks.action.${action}`)}</span>
              <span className="text-xs text-ink-muted">{t(`webhooks.actionNote.${action}`)}</span>
            </span>
          </label>
        ))}
        {actions.includes("search") && <p className="text-xs text-warn">{t("webhooks.searchWarning")}</p>}
      </fieldset>
      <fieldset className="flex flex-col gap-1">
        <legend className="text-sm font-medium">{t("webhooks.libraries")}</legend>
        <p className="text-xs text-ink-muted">{t("webhooks.inLibrariesNote")}</p>
        <div className="grid gap-1 sm:grid-cols-2">
          {(categories.data ?? []).map((category) => (
            <label key={category.id} className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={libraries.includes(category.id)} onChange={() => setLibraries(toggled(libraries, category.id))} />
              {category.name}
            </label>
          ))}
        </div>
      </fieldset>
      {actions.includes("playback") && (
        <label className={labelStyle}>
          {t("webhooks.mediaServer")}
          <select className={inputStyle} value={serverId} onChange={(event) => setServerId(Number(event.target.value))}>
            <option value={0}>{t("webhooks.allServers")}</option>
            {syncing.map((server) => (
              <option key={server.id} value={server.id}>{server.name}</option>
            ))}
          </select>
        </label>
      )}
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} />
        {t("webhooks.enabled")}
      </label>
      {failure && <p className="text-sm text-danger">{failure}</p>}
      <div className="flex gap-2">
        <Button variant="primary" busy={save.isPending} disabled={!name.trim() || actions.length === 0} onClick={() => { setFailure(undefined); save.mutate(); }}>
          {t("webhooks.save")}
        </Button>
        <Button variant="ghost" onClick={onCancel}>{t("webhooks.cancel")}</Button>
      </div>
    </section>
  );
}

function CopyLine({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-ink-muted">{label}</span>
      <div className="flex items-center gap-2">
        <input readOnly value={value} aria-label={label} onFocus={(event) => event.target.select()} className="min-w-0 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 font-mono text-[0.6875rem]" />
        <Button onClick={() => void navigator.clipboard?.writeText(value).then(() => setCopied(true))}>{copied ? t("webhooks.copied") : t("webhooks.copy")}</Button>
      </div>
    </div>
  );
}

/** The token, once: the address, a curl line per allowed action, and the two fields Jellyfin's Webhook plugin takes. */
function TokenPanel({ issued, onClose }: { issued: { webhook: Inbound; token: string }; onClose: () => void }) {
  const address = `${window.location.origin}${API_PREFIX}/hooks/${issued.webhook.id}`;
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-accent bg-surface p-4" role="status" aria-label={t("webhooks.tokenTitle")}>
      <h3 className="text-base font-semibold">{t("webhooks.tokenTitle")}</h3>
      <p className="text-sm text-warn">{t("webhooks.tokenOnce")}</p>
      <CopyLine label={t("webhooks.token")} value={issued.token} />
      <CopyLine label={t("webhooks.hookAddress")} value={address} />
      {issued.webhook.actions.map((action) => (
        <CopyLine key={action} label={t("webhooks.curlFor", { action: t(`webhooks.action.${action}`) })} value={curlLine(window.location.origin, issued.webhook.id, issued.token, action)} />
      ))}
      {issued.webhook.actions.includes("playback") && (
        <>
          <p className="text-xs text-ink-muted">{t("webhooks.jellyfinNote")}</p>
          <CopyLine label={t("webhooks.jellyfinUrl")} value={address} />
          <CopyLine label={t("webhooks.jellyfinHeader")} value={`Authorization: Bearer ${issued.token}`} />
        </>
      )}
      <div>
        <Button onClick={onClose}>{t("webhooks.tokenDone")}</Button>
      </div>
    </div>
  );
}
