import { useRef, useState } from "react";
import { PageHeader } from "@/ui/PageHeader";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useVirtualizer } from "@tanstack/react-virtual";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, type MessageKey } from "@/lib/i18n";
import { Button } from "@/ui/Button";
import { Chip, type ChipTone } from "@/ui/Chip";
import { Dialog } from "@/ui/Dialog";
import { SubTabs } from "@/ui/SubTabs";

type Kind = components["schemas"]["ProviderDefinitionKind"];
type Definition = components["schemas"]["ProviderDefinition"];
type Problem = components["schemas"]["ProviderDefinitionProblem"];

const KINDS: readonly Kind[] = ["metadata", "indexer", "download", "webhook"];
const ORIGINS = ["builtin", "custom", "override"] as const;
const STATES = ["active", "off", "needs_approval", "error"] as const;
const ROW_HEIGHT = 56;
const STATE_TONE: Record<Definition["state"], ChipTone> = { active: "ok", off: "neutral", needs_approval: "warn", error: "danger" };

/** The state chip's tone and word; shared with the tests. */
export function stateChip(state: Definition["state"]): { tone: ChipTone; label: string } {
  return { tone: STATE_TONE[state], label: t(`providerDefs.state.${state}`) };
}

/** Settings > Providers: every definition file by kind, built in and custom. */
export function ProviderDefinitionsPage() {
  const queryClient = useQueryClient();
  const [notice, setNotice] = useState("");
  const reload = useMutation({
    mutationFn: () => ok(api.POST("/provider-definitions/reload")),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["provider-definitions"] });
      setNotice(t("providerDefs.reloaded"));
    },
  });
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-6xl flex-col gap-4 p-6">
        <header className="flex flex-col gap-1">
          <PageHeader title={t("providerDefs.title")} />
          <p className="text-sm text-ink-muted">{t("providerDefs.intro")}</p>
        </header>
        <div className="flex flex-wrap items-center gap-3 rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
          <span className="min-w-0 flex-1">{t("providerDefs.folder")}</span>
          <Button busy={reload.isPending} onClick={() => reload.mutate()}>
            {t("providerDefs.reload")}
          </Button>
          {notice && <span role="status">{notice}</span>}
        </div>
        <SubTabs tabs={KINDS.map((kind) => ({ key: kind, label: t(`providerDefs.tab.${kind}`), content: <DefinitionTable kind={kind} /> }))} />
      </div>
    </div>
  );
}

function filterOf<T extends string>(value: unknown, allowed: readonly T[]): T | undefined {
  return allowed.find((one) => one === value);
}

function DefinitionTable({ kind }: { kind: Kind }) {
  const search = useSearch({ strict: false }) as { origin?: string; state?: string; q?: string };
  const navigate = useNavigate();
  const origin = filterOf(search.origin, ORIGINS);
  const state = filterOf(search.state, STATES);
  const list = useQuery({ queryKey: ["provider-definitions", kind], queryFn: () => ok(api.GET("/provider-definitions", { params: { query: { kind } } })) });
  const [editing, setEditing] = useState<{ definition: Definition; text: string } | undefined>();
  const [approving, setApproving] = useState<Definition>();
  const [testing, setTesting] = useState<Definition>();
  const [keys, setKeys] = useState<Definition>();
  const queryClient = useQueryClient();
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["provider-definitions"] });
  const act = useMutation({
    mutationFn: async (action: { name: "enable" | "disable" | "remove"; definition: Definition }) => {
      const params = { params: { path: { kind, key: action.definition.key } } };
      if (action.name === "enable") return ok(api.POST("/provider-definitions/{kind}/{key}/enable", params));
      if (action.name === "disable") return ok(api.POST("/provider-definitions/{kind}/{key}/disable", params));
      return ok(api.DELETE("/provider-definitions/{kind}/{key}", params));
    },
    onSuccess: refresh,
  });
  const open = useMutation({
    mutationFn: async (definition: Definition) => ({ definition, text: (await ok(api.GET("/provider-definitions/{kind}/{key}", { params: { path: { kind, key: definition.key } } }))).text }),
    onSuccess: setEditing,
  });
  const needle = String(search.q ?? "").trim().toLowerCase();
  const rows = (list.data ?? []).filter(
    (row) =>
      (origin === undefined || row.origin === origin) &&
      (state === undefined || row.state === state) &&
      (needle === "" || [row.name, row.key, row.description, ...row.hosts, ...row.media_types].some((text) => text.toLowerCase().includes(needle))),
  );
  const scrollRef = useRef<HTMLDivElement>(null);
  const virtualizer = useVirtualizer({ count: rows.length, getScrollElement: () => scrollRef.current, estimateSize: () => ROW_HEIGHT, overscan: 8 });
  const setFilter = (name: "origin" | "state" | "q", value: string) =>
    void navigate({ to: ".", replace: true, search: (previous: Record<string, unknown>) => ({ ...previous, [name]: value || undefined }) });
  return (
    <div className="flex flex-col gap-3">
      <p className="text-xs text-ink-muted">{t("providerDefs.catalogNote")}</p>
      <div className="flex flex-wrap items-center gap-3 text-xs text-ink-muted">
        <input
          type="search"
          aria-label={t("providerDefs.search")}
          placeholder={t("providerDefs.search")}
          className="w-56 rounded-md border border-border-subtle bg-surface px-2 py-1 text-ink"
          value={String(search.q ?? "")}
          onChange={(event) => setFilter("q", event.target.value)}
        />
        <label className="flex items-center gap-1">
          {t("providerDefs.filter.origin")}
          <select className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-ink" value={origin ?? ""} onChange={(event) => setFilter("origin", event.target.value)}>
            <option value="">{t("providerDefs.filter.all")}</option>
            {ORIGINS.map((one) => (
              <option key={one} value={one}>
                {t(`providerDefs.origin.${one}`)}
              </option>
            ))}
          </select>
        </label>
        <label className="flex items-center gap-1">
          {t("providerDefs.filter.state")}
          <select className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-ink" value={state ?? ""} onChange={(event) => setFilter("state", event.target.value)}>
            <option value="">{t("providerDefs.filter.all")}</option>
            {STATES.map((one) => (
              <option key={one} value={one}>
                {t(`providerDefs.state.${one}`)}
              </option>
            ))}
          </select>
        </label>
        <span className="tabular-nums">{rows.length}</span>
      </div>
      {(act.isError || open.isError) && <p className="text-xs text-danger">{t("providerDefs.saveFailed")}</p>}
      <div ref={scrollRef} className="h-[min(60vh,40rem)] overflow-y-auto rounded-md border border-border-subtle">
        {rows.length === 0 && !list.isPending && <p className="p-4 text-sm text-ink-muted">{t("providerDefs.empty")}</p>}
        <div className="relative" style={{ height: virtualizer.getTotalSize() }}>
          {virtualizer.getVirtualItems().map((item) => {
            const row = rows[item.index];
            if (!row) return null;
            const chip = stateChip(row.state);
            const custom = row.origin !== "builtin";
            return (
              <div
                key={row.key}
                className="absolute inset-x-0 flex items-center gap-2 border-b border-border-subtle px-3 text-sm"
                style={{ height: item.size, transform: `translateY(${item.start}px)` }}
              >
                <div className="min-w-0 flex-1">
                  <div className="truncate font-medium">{row.name}</div>
                  <div className="truncate font-mono text-[0.6875rem] text-ink-muted">
                    {row.key}
                    {row.engine ? ` - ${row.engine}` : ""}
                    {row.hosts.length > 0 ? ` - ${row.hosts.join(", ")}` : ""}
                  </div>
                </div>
                <Chip tone={custom ? "accent" : "neutral"}>{t(`providerDefs.origin.${row.origin}`)}</Chip>
                <Chip tone={chip.tone} title={row.problems.map((problem) => `${t(`providerDefs.problem.${problem.code}` as MessageKey)}`).join(" ")}>
                  {chip.label}
                </Chip>
                <div className="flex flex-wrap items-center justify-end gap-1">
                  {row.state === "needs_approval" && <Button onClick={() => setApproving(row)}>{t("providerDefs.approve")}</Button>}
                  {row.state === "active" && <Button onClick={() => act.mutate({ name: "disable", definition: row })}>{t("providerDefs.disable")}</Button>}
                  {row.state === "off" && <Button onClick={() => act.mutate({ name: "enable", definition: row })}>{t("providerDefs.enable")}</Button>}
                  {kind === "metadata" && row.state === "active" && row.engine === "http" && <Button onClick={() => setTesting(row)}>{t("providerDefs.test")}</Button>}
                  {row.settings.length > 0 && <Button onClick={() => setKeys(row)}>{t("providerDefs.values")}</Button>}
                  <Button busy={open.isPending} onClick={() => open.mutate(row)}>
                    {custom ? t("providerDefs.edit") : t("providerDefs.copy")}
                  </Button>
                  {custom && row.state !== "error" && (
                    <Button variant="danger" onClick={() => act.mutate({ name: "remove", definition: row })}>
                      {row.origin === "override" ? t("providerDefs.revert") : t("providerDefs.remove")}
                    </Button>
                  )}
                  {row.state === "error" && (
                    <Button variant="danger" onClick={() => act.mutate({ name: "remove", definition: row })}>
                      {t("providerDefs.remove")}
                    </Button>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>
      {editing && <EditDialog kind={kind} {...editing} onClose={() => setEditing(undefined)} onSaved={() => void refresh()} />}
      {approving && (
        <Dialog
          title={t("providerDefs.approveTitle", { name: approving.name })}
          description={t("providerDefs.approveBody")}
          onClose={() => setApproving(undefined)}
          actions={
            <Button
              variant="primary"
              busy={act.isPending}
              onClick={() => {
                act.mutate({ name: "enable", definition: approving });
                setApproving(undefined);
              }}
            >
              {t("providerDefs.enable")}
            </Button>
          }
        >
          <p className="text-xs text-ink-muted">{t("providerDefs.hosts")}</p>
          <ul className="font-mono text-sm">
            {approving.hosts.length === 0 ? <li>{t("providerDefs.noHosts")}</li> : approving.hosts.map((host) => <li key={host}>{host}</li>)}
          </ul>
        </Dialog>
      )}
      {testing && <TestDialog kind={kind} definition={testing} onClose={() => setTesting(undefined)} />}
      {keys && <KeysDialog kind={kind} definition={keys} onClose={() => setKeys(undefined)} onSaved={() => void refresh()} />}
    </div>
  );
}

function ProblemList({ problems }: { problems: Problem[] }) {
  return (
    <ul className="flex flex-col gap-1 text-xs">
      {problems.map((problem, index) => (
        <li key={index} className={problem.severity === "error" ? "text-danger" : "text-warn"}>
          {problem.line > 0 && <span className="font-mono">{t("providerDefs.problemAt", { line: problem.line, column: problem.column })}: </span>}
          {t(`providerDefs.problem.${problem.code}` as MessageKey)}
          {problem.field ? ` ${problem.field}` : ""}
        </li>
      ))}
    </ul>
  );
}

function EditDialog({ kind, definition, text, onClose, onSaved }: { kind: Kind; definition: Definition; text: string; onClose: () => void; onSaved: () => void }) {
  const [draft, setDraft] = useState(text);
  const [problems, setProblems] = useState<Problem[]>([]);
  const [message, setMessage] = useState("");
  const send = useMutation({
    mutationFn: (dryRun: boolean) =>
      ok(api.PUT("/provider-definitions/{kind}/{key}", { params: { path: { kind, key: definition.key }, query: { dry_run: dryRun } }, body: { text: draft } })),
    onSuccess: (result, dryRun) => {
      setProblems(result.problems);
      setMessage(result.saved ? t("providerDefs.saved") : result.problems.some((problem) => problem.severity === "error") ? "" : dryRun ? t("providerDefs.valid") : "");
      if (result.saved) {
        onSaved();
        onClose();
      }
    },
  });
  return (
    <Dialog
      title={t("providerDefs.editTitle", { key: definition.key })}
      description={t("providerDefs.editHelp")}
      width="wide"
      onClose={onClose}
      actions={
        <>
          <Button busy={send.isPending} onClick={() => send.mutate(true)}>
            {t("providerDefs.validate")}
          </Button>
          <Button variant="primary" busy={send.isPending} onClick={() => send.mutate(false)}>
            {t("providerDefs.save")}
          </Button>
        </>
      }
    >
      <textarea
        aria-label={t("providerDefs.editTitle", { key: definition.key })}
        className="h-[min(50vh,28rem)] w-full resize-y rounded-md border border-border-subtle bg-surface p-2 font-mono text-xs"
        spellCheck={false}
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
      />
      {message && <p role="status" className="text-xs text-ok">{message}</p>}
      <ProblemList problems={problems} />
    </Dialog>
  );
}

function TestDialog({ kind, definition, onClose }: { kind: Kind; definition: Definition; onClose: () => void }) {
  const [title, setTitle] = useState("");
  const run = useMutation({
    mutationFn: () => ok(api.POST("/provider-definitions/{kind}/{key}/test", { params: { path: { kind, key: definition.key } }, body: { title } })),
  });
  const result = run.data;
  return (
    <Dialog
      title={t("providerDefs.testTitle", { name: definition.name })}
      description={t("providerDefs.testHelp")}
      width="wide"
      onClose={onClose}
      actions={
        <Button variant="primary" busy={run.isPending} disabled={title.trim() === ""} onClick={() => run.mutate()}>
          {t("providerDefs.testRun")}
        </Button>
      }
    >
      <input
        aria-label={t("providerDefs.testTitleField")}
        placeholder={t("providerDefs.testTitleField")}
        className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-sm"
        value={title}
        onChange={(event) => setTitle(event.target.value)}
      />
      {result && (
        <div className="flex flex-col gap-2 text-xs">
          <p role="status" className={result.ok ? "text-ok" : "text-danger"}>
            {result.ok ? t("providerDefs.testOk") : t("providerDefs.testFailed", { code: result.error_code ?? "" })}
            {result.error ? ` ${result.error}` : ""}
          </p>
          <p className="font-medium">{t("providerDefs.testRequests")}</p>
          <ul className="font-mono">
            {result.requests.map((request, index) => (
              <li key={index}>
                {request.method} {request.host}
                {request.path} - {request.status} - {request.millis} ms
              </li>
            ))}
          </ul>
          <p className="font-medium">{t("providerDefs.testRecords")}</p>
          <ul className="flex flex-col gap-1">
            {result.records.map((record, index) => (
              <li key={index}>
                {record.item ? "* " : ""}
                {record.title} {record.year > 0 ? `(${record.year})` : ""} <span className="font-mono text-ink-muted">{JSON.stringify(record.ids)}</span>
              </li>
            ))}
          </ul>
        </div>
      )}
    </Dialog>
  );
}

function KeysDialog({ kind, definition, onClose, onSaved }: { kind: Kind; definition: Definition; onClose: () => void; onSaved: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const save = useMutation({
    mutationFn: () => {
      const sent: Record<string, string> = {};
      for (const [name, value] of Object.entries(values)) if (value !== "") sent[name] = value.trim();
      return ok(api.PUT("/provider-definitions/{kind}/{key}/settings", { params: { path: { kind, key: definition.key } }, body: { values: sent } }));
    },
    onSuccess: () => {
      onSaved();
      onClose();
    },
  });
  return (
    <Dialog
      title={t("providerDefs.valuesTitle", { name: definition.name })}
      description={t("providerDefs.valuesHelp")}
      onClose={onClose}
      actions={
        <Button variant="primary" busy={save.isPending} onClick={() => save.mutate()}>
          {t("providerDefs.save")}
        </Button>
      }
    >
      {definition.settings.map((setting) => (
        <label key={setting.name} className="flex flex-col gap-1 text-xs text-ink-muted">
          {setting.label} ({setting.is_set ? t("providerDefs.valueSet") : t("providerDefs.valueUnset")})
          <input
            type={setting.type === "password" ? "password" : "text"}
            autoComplete="off"
            className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-sm text-ink"
            value={values[setting.name] ?? ""}
            onChange={(event) => setValues({ ...values, [setting.name]: event.target.value })}
          />
        </label>
      ))}
      {save.isError && <p className="text-xs text-danger">{t("providerDefs.saveFailed")}</p>}
    </Dialog>
  );
}
