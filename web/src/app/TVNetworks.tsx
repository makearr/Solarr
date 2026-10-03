import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { FollowPanel } from "./FollowPanel";
import { SeriesTile } from "./SeriesIndex";

type Network = components["schemas"]["SeriesNetwork"];

function Logo({ network, size }: { network: Network; size: string }) {
  return network.logo_url ? (
    <img src={network.logo_url} alt="" loading="lazy" className={`${size} object-contain`} referrerPolicy="no-referrer" />
  ) : (
    <Radio aria-hidden className="size-8 text-ink-muted" />
  );
}

/** A TV library's Networks level: where its series air, most first. */
export function TVNetworks() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const networks = useQuery({
    queryKey: ["series-networks", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/series/networks", { params: { query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="flex flex-col gap-4 p-4">
      <h1 className="text-lg font-semibold">Networks</h1>
      {networks.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {networks.isError && <p className="text-sm text-danger">The networks could not be loaded.</p>}
      {networks.data && networks.data.length === 0 && <p className="text-sm text-ink-muted">No series with a known network yet.</p>}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(10rem,1fr))] gap-3">
        {(networks.data ?? []).map((network) => (
          <Link
            key={network.id}
            to="/library/$categoryId/networks/$networkId"
            params={{ categoryId, networkId: String(network.id) }}
            className="flex flex-col items-center gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-center hover:border-accent
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <span className="flex h-12 items-center justify-center">
              <Logo network={network} size="max-h-12 max-w-32" />
            </span>
            <span className="w-full truncate text-sm font-medium">{network.name}</span>
            <span className="text-xs text-ink-muted">{network.series_count} series</span>
          </Link>
        ))}
      </div>
    </div>
  );
}

/** One network and the library's series on it. */
export function TVNetwork() {
  const { categoryId, networkId } = useParams({ strict: false }) as { categoryId: string; networkId: string };
  const detail = useQuery({
    queryKey: ["series-network", categoryId, networkId],
    queryFn: async () => {
      const { data, error } = await api.GET("/series/networks/{id}", {
        params: { path: { id: Number(networkId) }, query: { category_id: Number(categoryId) } },
      });
      if (error) throw error;
      return data;
    },
  });
  if (detail.isLoading) return <p className="p-4 text-sm text-ink-muted">Loading…</p>;
  if (detail.isError || !detail.data) return <p className="p-4 text-sm text-danger">This network could not be found.</p>;

  const { network, series, follow, followable } = detail.data;
  return (
    <div className="flex flex-col gap-4 p-4">
      <Link to="/library/$categoryId/$level" params={{ categoryId, level: "networks" }} className="text-xs text-ink-muted hover:text-ink">
        ← Networks
      </Link>
      <div className="flex items-center gap-4">
        <Logo network={network} size="max-h-14 max-w-40" />
        <div>
          <h1 className="text-xl font-semibold">{network.name}</h1>
          <p className="text-sm text-ink-muted">
            {[network.country?.toUpperCase(), `${network.series_count} series in this library`].filter(Boolean).join(" · ")}
          </p>
        </div>
      </div>
      <FollowPanel kind="network" subjectId={network.id} categoryId={categoryId} follow={follow} followable={followable} />
      <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-4">
        {series.map((entry) => (
          <SeriesTile key={entry.id} series={entry} categoryId={categoryId} />
        ))}
      </div>
    </div>
  );
}
