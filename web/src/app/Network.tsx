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
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Network</h2>
      <p className="text-xs text-ink-muted">
        The listen port, a global proxy and an IP whitelist. The whitelist can only deny --
        it never authenticates and never grants.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <NetworkForm
          network={network.data}
          onSave={(body) => update.mutate(body)}
          busy={update.isPending}
        />
        {update.isError && (
          <p className="text-xs text-danger">
            That change was refused -- often because it would exclude the address you are using
            right now.
          </p>
        )}
      </div>
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
      className="flex flex-col gap-3 text-xs text-ink-muted"
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
      <label className="flex flex-col gap-1">
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
        {network.restart_required && (
          <span className="flex items-center gap-1.5 text-[0.6875rem] text-amber-600 dark:text-amber-400">
            <AlertTriangle aria-hidden className="size-3.5" />
            Port {network.port} is saved but not live yet -- restart the container.
          </span>
        )}
      </label>

      <Toggle
        label="Send outbound traffic through a proxy"
        checked={proxyEnabled}
        disabled={busy}
        onChange={setProxyEnabled}
      />

      <label className="flex flex-col gap-1">
        Proxy
        <input
          type="text"
          placeholder="socks5://host:1080"
          value={proxyURL}
          disabled={busy}
          onChange={(event) => setProxyURL(event.target.value)}
          className={`${inputStyle} w-64`}
        />
        <span className="text-[0.6875rem]">http, https or socks5, with a port. Used only while the box above is ticked.</span>
      </label>

      <label className="flex flex-col gap-1">
        Proxy bypass
        <textarea
          rows={3}
          placeholder={"10.0.0.0/8\n.lan"}
          value={proxyBypass}
          disabled={busy}
          onChange={(event) => setProxyBypass(event.target.value)}
          className={`${inputStyle} w-64 font-mono`}
        />
        <span className="text-[0.6875rem]">
          One CIDR or host suffix (".lan") per line. These always go direct.
        </span>
      </label>

      <Toggle
        label="Only allow these addresses to reach this instance"
        checked={allowEnabled}
        disabled={busy}
        onChange={setAllowEnabled}
      />

      <label className="flex flex-col gap-1">
        IP whitelist
        <textarea
          rows={3}
          placeholder={"10.0.0.0/8\n192.168.1.0/24"}
          value={allowCIDRs}
          disabled={busy}
          onChange={(event) => setAllowCIDRs(event.target.value)}
          className={`${inputStyle} w-64 font-mono`}
        />
        <span className="text-[0.6875rem]">
          One CIDR per line, used only while the box above is ticked. /api/v1/health always stays
          reachable, and enabling a list that would exclude the address you're using right now is
          refused outright.
        </span>
      </label>

      {dirty && (
        <button
          type="submit"
          disabled={busy}
          className="w-fit rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Save
        </button>
      )}
    </form>
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
    <label className="flex items-center gap-2 text-ink">
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
      {label}
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
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
