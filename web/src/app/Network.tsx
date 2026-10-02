import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";

import { api } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

type NetworkSettings = components["schemas"]["NetworkSettings"];
type NetworkUpdate = components["schemas"]["NetworkSettingsUpdate"];

/** todo 4.19, ADR-58: the listen port, the global egress proxy and the IP
 *  allowlist. The allowlist can only ever deny -- it never authenticates and
 *  never grants (Invariant 89) -- and a change that would lock out the
 *  address making it is refused outright. Advanced tier (the registry's own
 *  network.* keys, todo 4.14): a working library needs none of this
 *  touched, so a normal-tier caller does not see the section at all. */
export function Network() {
  const tier = useTier();
  const queryClient = useQueryClient();
  const network = useQuery({
    queryKey: ["network"],
    queryFn: async () => {
      const { data, error } = await api.GET("/network");
      if (error) throw error;
      return data;
    },
  });

  const update = useMutation({
    mutationFn: async (body: NetworkUpdate) => {
      const { error } = await api.PATCH("/network", { body });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["network"] }),
  });

  if (!isTierVisible(tier, "advanced")) return null;
  if (network.error !== null || !network.data) return null;

  return (
    <section className="flex flex-col gap-3">
      <p className="max-w-2xl text-sm text-ink-muted">
        Where Solarr listens, how it reaches the internet, and who may reach it. The IP whitelist can only deny: it never
        authenticates and never grants.
      </p>
      <NetworkForm network={network.data} onSave={(body) => update.mutate(body)} busy={update.isPending} />
      {update.isError && (
        <p className="text-sm text-danger">
          That change was refused, often because it would exclude the address you are using right now.
        </p>
      )}
    </section>
  );
}

function NetworkForm({
  network,
  onSave,
  busy,
}: {
  network: NetworkSettings;
  onSave: (body: NetworkUpdate) => void;
  busy: boolean;
}) {
  // The field carries the port actually being served, not the stored 0 that
  // means "config.yml governs" (asked for 2026-09-19).
  const [port, setPort] = useState(network.port === 0 ? network.in_effect_port : network.port);
  const [proxyEnabled, setProxyEnabled] = useState(network.proxy_enabled);
  const [proxyURL, setProxyURL] = useState(network.proxy_url ?? "");
  const [proxyBypass, setProxyBypass] = useState(network.proxy_bypass.join("\n"));
  const [allowEnabled, setAllowEnabled] = useState(network.allow_cidrs_enabled);
  const [allowCIDRs, setAllowCIDRs] = useState(network.allow_cidrs.join("\n"));

  const parsedBypass = splitLines(proxyBypass);
  const parsedAllow = splitLines(allowCIDRs);

  const dirty =
    port !== (network.port === 0 ? network.in_effect_port : network.port) ||
    proxyEnabled !== network.proxy_enabled ||
    allowEnabled !== network.allow_cidrs_enabled ||
    proxyURL !== (network.proxy_url ?? "") ||
    parsedBypass.join("\n") !== network.proxy_bypass.join("\n") ||
    parsedAllow.join("\n") !== network.allow_cidrs.join("\n");

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(event) => {
        event.preventDefault();
        onSave({
          port,
          proxy_enabled: proxyEnabled,
          proxy_url: proxyURL.trim() === "" ? undefined : proxyURL.trim(),
          proxy_bypass: parsedBypass,
          allow_cidrs_enabled: allowEnabled,
          allow_cidrs: parsedAllow,
        });
      }}
    >
      <Card title="Listen port" help="The port Solarr serves on. A change needs a restart of the container.">
        <label className="flex w-40 flex-col gap-1 text-xs text-ink-muted">
          Port
          <input
            type="number"
            min={0}
            max={65535}
            value={port}
            disabled={busy}
            onChange={(event) => setPort(Number(event.target.value))}
            className={inputStyle}
          />
        </label>
        {network.restart_required && (
          <p className="flex items-center gap-1.5 text-xs text-amber-600 dark:text-amber-400">
            <AlertTriangle aria-hidden className="size-3.5" />
            Port {network.port} is saved but not live yet: restart the container.
          </p>
        )}
      </Card>

      <Card
        title="Outbound proxy"
        help="Send Solarr's own internet traffic (metadata providers, indexers) through a proxy. Addresses in the bypass list always go direct."
        toggle={<Toggle label="Use a proxy" checked={proxyEnabled} disabled={busy} onChange={setProxyEnabled} />}
      >
        <div className={`flex flex-col gap-3 ${proxyEnabled ? "" : "opacity-60"}`}>
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            Proxy address
            <input
              type="text"
              placeholder="socks5://host:1080"
              value={proxyURL}
              disabled={busy}
              onChange={(event) => setProxyURL(event.target.value)}
              className={`${inputStyle} max-w-md`}
            />
            <span>http, https or socks5, with a port.</span>
          </label>
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            Bypass
            <textarea
              rows={4}
              placeholder={"10.0.0.0/8\n.lan"}
              value={proxyBypass}
              disabled={busy}
              onChange={(event) => setProxyBypass(event.target.value)}
              className={`${inputStyle} max-w-md font-mono`}
            />
            <span>One network (CIDR) or host suffix (.lan) per line.</span>
          </label>
        </div>
      </Card>

      <Card
        title="IP whitelist"
        help="Only these addresses may reach Solarr. /api/v1/health always stays reachable, and a list that would exclude the address you are using now is refused."
        toggle={<Toggle label="Only allow these addresses" checked={allowEnabled} disabled={busy} onChange={setAllowEnabled} />}
      >
        <label className={`flex flex-col gap-1 text-xs text-ink-muted ${allowEnabled ? "" : "opacity-60"}`}>
          Allowed networks
          <textarea
            rows={4}
            placeholder={"10.0.0.0/8\n192.168.1.0/24"}
            value={allowCIDRs}
            disabled={busy}
            onChange={(event) => setAllowCIDRs(event.target.value)}
            className={`${inputStyle} max-w-md font-mono`}
          />
          <span>One network (CIDR) per line.</span>
        </label>
      </Card>

      <div className="sticky bottom-0 -mx-1 flex items-center gap-3 border-t border-border-subtle bg-canvas/95 px-1 py-3 backdrop-blur">
        <button
          type="submit"
          disabled={busy || !dirty}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {busy ? "Saving…" : "Save"}
        </button>
        {dirty && <span className="text-xs text-ink-muted">Unsaved changes</span>}
      </div>
    </form>
  );
}

/** One group of related settings: a heading, what it does, an optional on/off switch and the fields. */
function Card({
  title,
  help,
  toggle,
  children,
}: {
  title: string;
  help: string;
  toggle?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
      <div className="flex items-start gap-4">
        <div className="flex flex-1 flex-col gap-0.5">
          <h2 className="text-sm font-medium">{title}</h2>
          <p className="max-w-xl text-xs text-ink-muted">{help}</p>
        </div>
        {toggle}
      </div>
      {children}
    </section>
  );
}

function Toggle({
  label,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  checked: boolean;
  disabled: boolean;
  onChange: (value: boolean) => void;
}) {
  return (
    <label className="flex shrink-0 cursor-pointer items-center gap-2 text-xs text-ink">
      {label}
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
        className="h-5 w-9 cursor-pointer appearance-none rounded-full bg-border-subtle transition-colors
                   checked:bg-accent relative after:absolute after:left-0.5 after:top-0.5 after:size-4
                   after:rounded-full after:bg-white after:transition-transform checked:after:translate-x-4"
      />
    </label>
  );
}

function splitLines(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
