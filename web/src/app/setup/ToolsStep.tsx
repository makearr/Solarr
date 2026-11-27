import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { kindSecret } from "@/lib/downloaderKinds";
import { t } from "@/lib/i18n";
import { useDownloaderKinds } from "@/lib/resources";

import { ConnectionForm, useDiscovered } from "./ConnectStep";
import { StepFrame } from "./StepFrame";
import { Field, Problem, Verdict, inputStyle, primaryButtonStyle, quietButtonStyle } from "./ui";
import type { WizardState } from "./wizardState";

type Result = components["schemas"]["ConnectionTestResult"];

const CLIENT_PORT: Record<string, number> = { sabnzbd: 8080, qbittorrent: 8080 };

/** The fresh path's search and download tools: Prowlarr or one indexer, and SABnzbd or qBittorrent; each is tested before it is kept. */
export function ToolsStep({ state, update, onBack, onNext }: { state: WizardState; update: (next: WizardState) => void; onBack: () => void; onNext: () => void }) {
  const prowlarr = state.connections.find((connection) => connection.app === "prowlarr");
  return (
    <StepFrame title={t("setup.tools.title")} description={t("setup.tools.description")} onBack={onBack} onNext={onNext} onSkip={onNext}>
      <section className="space-y-3">
        <h3 className="text-sm font-medium">{t("setup.tools.indexers")}</h3>
        <p className="text-xs text-ink-muted">{t("setup.tools.indexersHelp")}</p>
        {prowlarr ? (
          <div className="flex items-center gap-2 text-xs">
            <Verdict ok>{t("setup.tools.prowlarrAdded", { name: prowlarr.name })}</Verdict>
            <button type="button" onClick={() => update({ ...state, connections: state.connections.filter((one) => one !== prowlarr) })} className={quietButtonStyle}>
              {t("setup.remove")}
            </button>
          </div>
        ) : (
          <ConnectionForm initial={{ app: "prowlarr", baseUrl: "" }} only={["prowlarr"]} state={state} onAdded={update} />
        )}
        <IndexerForm />
      </section>
      <section className="space-y-3">
        <h3 className="text-sm font-medium">{t("setup.tools.clients")}</h3>
        <p className="text-xs text-ink-muted">{t("setup.tools.clientsHelp")}</p>
        <ClientForm category={`solarr-${state.libraries[0]?.mediaType ?? "media"}`} />
      </section>
    </StepFrame>
  );
}

function resultLine(result: Result | undefined) {
  if (!result) return null;
  return result.ok ? <Verdict ok>{result.message}</Verdict> : <Verdict ok={false}>{result.message}</Verdict>;
}

function IndexerForm() {
  const queryClient = useQueryClient();
  const [impl, setImpl] = useState<"newznab" | "torznab">("newznab");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [tested, setTested] = useState<{ signature: string; result: Result } | null>(null);
  const [added, setAdded] = useState<string[]>([]);
  const signature = `${impl}|${baseUrl.trim()}|${apiKey.trim()}`;
  const verdict = tested?.signature === signature ? tested.result : undefined;
  const test = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/indexers/test", { body: { impl, base_url: baseUrl.trim(), api_key: apiKey.trim() || undefined } })),
    onSuccess: (result) => setTested({ signature, result }),
  });
  const add = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/indexers", { body: { name: name.trim() || baseUrl.trim(), impl, base_url: baseUrl.trim(), api_key: apiKey.trim() || undefined, enabled: true, priority: 25 } })),
    onSuccess: async (indexer) => {
      await queryClient.invalidateQueries({ queryKey: ["indexers"] });
      setAdded((before) => [...before, indexer.name]);
      setName("");
      setBaseUrl("");
      setApiKey("");
      setTested(null);
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
      <p className="text-xs font-medium text-ink-muted">{t("setup.tools.oneIndexer")}</p>
      {added.map((one) => (
        <Verdict key={one} ok>{t("setup.tools.indexerAdded", { name: one })}</Verdict>
      ))}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("setup.tools.indexerType")}>
          <select className={inputStyle} value={impl} onChange={(event) => setImpl(event.target.value as typeof impl)}>
            <option value="newznab">{t("setup.tools.usenet")}</option>
            <option value="torznab">{t("setup.tools.torrent")}</option>
          </select>
        </Field>
        <Field label={t("setup.connect.name")}>
          <input className={inputStyle} value={name} autoComplete="off" onChange={(event) => setName(event.target.value)} />
        </Field>
      </div>
      <Field label={t("setup.connect.address")}>
        <input className={inputStyle} value={baseUrl} placeholder="https://indexer.example" autoComplete="off" onChange={(event) => setBaseUrl(event.target.value)} />
      </Field>
      <Field label={t("setup.connect.key")}>
        <input className={inputStyle} type="password" value={apiKey} autoComplete="off" onChange={(event) => setApiKey(event.target.value)} />
      </Field>
      {resultLine(verdict)}
      {test.isError && <Problem error={test.error} />}
      {add.isError && <Problem error={add.error} />}
      <div className="flex gap-2">
        <button type="button" disabled={!baseUrl.trim() || test.isPending} onClick={() => test.mutate()} className={quietButtonStyle}>
          {test.isPending ? t("setup.test.testing") : t("setup.test.button")}
        </button>
        <button type="submit" disabled={!verdict?.ok || add.isPending} className={primaryButtonStyle}>
          {t("setup.connect.add")}
        </button>
      </div>
    </form>
  );
}

function ClientForm({ category }: { category: string }) {
  const queryClient = useQueryClient();
  const kinds = useDownloaderKinds();
  const found = useDiscovered();
  const [impl, setImpl] = useState<"sabnzbd" | "qbittorrent">("sabnzbd");
  const kind = kinds.data?.find((one) => one.impl === impl);
  const suggestion = (found.data ?? []).find((service) => service.kind === impl)?.base_url;
  const [typedUrl, setTypedUrl] = useState<string>();
  const baseUrl = typedUrl ?? suggestion ?? "";
  const [username, setUsername] = useState("");
  const [secret, setSecret] = useState("");
  const [chosenCategory, setCategory] = useState(category);
  const [tested, setTested] = useState<{ signature: string; result: Result } | null>(null);
  const [added, setAdded] = useState<string[]>([]);
  const credentials = kindSecret(kind, username, secret);
  const signature = `${impl}|${baseUrl}|${credentials}|${chosenCategory}`;
  const verdict = tested?.signature === signature ? tested.result : undefined;
  const test = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () => ok(api.POST("/downloaders/test", { body: { impl, base_url: baseUrl.trim(), credentials: credentials || undefined, options: { category: chosenCategory || undefined } } })),
    onSuccess: (result) => setTested({ signature, result }),
  });
  const add = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () =>
      ok(api.POST("/downloaders", {
        body: {
          name: kind?.label ?? impl, impl, base_url: baseUrl.trim(), protocol: kind?.protocol ?? "usenet",
          credentials: credentials || undefined, enabled: true, options: { category: chosenCategory || undefined },
        },
      })),
    onSuccess: async (downloader) => {
      await queryClient.invalidateQueries({ queryKey: ["downloaders"] });
      setAdded((before) => [...before, downloader.name]);
      setTypedUrl("");
      setSecret("");
      setUsername("");
      setTested(null);
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
      {added.map((one) => (
        <Verdict key={one} ok>{t("setup.tools.clientAdded", { name: one })}</Verdict>
      ))}
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label={t("setup.tools.clientType")}>
          <select className={inputStyle} value={impl} onChange={(event) => { setImpl(event.target.value as typeof impl); setTypedUrl(undefined); }}>
            <option value="sabnzbd">SABnzbd</option>
            <option value="qbittorrent">qBittorrent</option>
          </select>
        </Field>
        <Field label={t("setup.clients.category")} hint={t("setup.clients.categoryHint")}>
          <input className={inputStyle} value={chosenCategory} autoComplete="off" onChange={(event) => setCategory(event.target.value)} />
        </Field>
      </div>
      <Field label={t("setup.connect.address")}>
        <input className={inputStyle} value={baseUrl} placeholder={`http://${impl}:${CLIENT_PORT[impl]}`} autoComplete="off" onChange={(event) => setTypedUrl(event.target.value)} />
      </Field>
      {kind?.user_name && (
        <Field label={t("setup.clients.username")}>
          <input className={inputStyle} value={username} autoComplete="off" onChange={(event) => setUsername(event.target.value)} />
        </Field>
      )}
      {kind?.secret_label && (
        <Field label={kind.secret_label}>
          <input className={inputStyle} type="password" value={secret} autoComplete="off" onChange={(event) => setSecret(event.target.value)} />
        </Field>
      )}
      {resultLine(verdict)}
      {test.isError && <Problem error={test.error} />}
      {add.isError && <Problem error={add.error} />}
      <div className="flex gap-2">
        <button type="button" disabled={!baseUrl.trim() || test.isPending} onClick={() => test.mutate()} className={quietButtonStyle}>
          {test.isPending ? t("setup.test.testing") : t("setup.test.button")}
        </button>
        <button type="submit" disabled={!verdict?.ok || add.isPending} className={primaryButtonStyle}>
          {t("setup.connect.add")}
        </button>
      </div>
    </form>
  );
}

