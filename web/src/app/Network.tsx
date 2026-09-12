import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";

import { api } from "@/api/client";
import { isTierVisible, useTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

type NetworkSettings = components["schemas"]["NetworkSettings"];

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
    mutationFn: async (body: {
      port: number;
      proxy_url?: string;
      proxy_bypass: string[];
      allow_cidrs: string[];
    }) => {
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
        Port resolution, a global egress proxy and an IP allowlist. The allowlist can only deny --
        it never authenticates and never grants.
      </p>

      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <PortRow network={network.data} />

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

function PortRow({ network }: { network: NetworkSettings }) {
  return (
    <div className="flex flex-col gap-1 border-b border-border-subtle pb-2.5 text-xs">
      <div className="flex items-center gap-2">
        <span className="font-medium">In effect</span>
        <span className="text-ink-muted">{network.in_effect_port}</span>
      </div>
      {network.restart_required && (
        <p className="flex items-center gap-1.5 text-amber-600 dark:text-amber-400">
          <AlertTriangle aria-hidden className="size-3.5" />
          Port {network.port} is saved but not live yet -- restart the container for it to take
          effect.
        </p>
      )}
    </div>
  );
}

function NetworkForm({
  network,
  onSave,
  busy,
}: {
  network: NetworkSettings;
  onSave: (body: { port: number; proxy_url?: string; proxy_bypass: string[]; allow_cidrs: string[] }) => void;
  busy: boolean;
}) {
  const [port, setPort] = useState(network.port);
  const [proxyURL, setProxyURL] = useState(network.proxy_url ?? "");
  const [proxyBypass, setProxyBypass] = useState(network.proxy_bypass.join("\n"));
  const [allowCIDRs, setAllowCIDRs] = useState(network.allow_cidrs.join("\n"));

  const parsedBypass = splitLines(proxyBypass);
  const parsedAllow = splitLines(allowCIDRs);

  const dirty =
    port !== network.port ||
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
          proxy_url: proxyURL.trim() === "" ? undefined : proxyURL.trim(),
          proxy_bypass: parsedBypass,
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
        <span className="text-[0.6875rem]">
          0 leaves it unset -- config.yml (or 5454) governs. A real choice is written into
          config.yml directly, not just here, so it survives however the next restart happens. If
          a chosen port turns out unreachable, the MEDIANEXUS_SERVER_PORT environment variable
          always wins over both and is the way back in.
        </span>
      </label>

      <label className="flex flex-col gap-1">
        Egress proxy URL
        <input
          type="text"
          placeholder="socks5://host:1080"
          value={proxyURL}
          disabled={busy}
          onChange={(event) => setProxyURL(event.target.value)}
          className={`${inputStyle} w-64`}
        />
        <span className="text-[0.6875rem]">Empty means no proxy. http, https or socks5, with a port.</span>
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

      <label className="flex flex-col gap-1">
        IP allowlist
        <textarea
          rows={3}
          placeholder={"10.0.0.0/8\n192.168.1.0/24"}
          value={allowCIDRs}
          disabled={busy}
          onChange={(event) => setAllowCIDRs(event.target.value)}
          className={`${inputStyle} w-64 font-mono`}
        />
        <span className="text-[0.6875rem]">
          One CIDR per line. Empty allows every address, the default. /api/v1/health always stays
          reachable, and a change that would exclude the address you're using right now is
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

function splitLines(value: string): string[] {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "");
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
