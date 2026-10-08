import { useRef } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Radio } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { FollowPanel } from "./FollowPanel";
import { SeriesPosterTile } from "./SeriesIndex";
import { remoteImage } from "@/lib/remoteImage";
import { usePreference } from "@/lib/usePreference";
import { PosterGrid } from "@/list/PosterGrid";
import { useGridMetrics } from "@/list/useGridMetrics";
import { useLibraryList } from "@/list/useLibraryList";
import { useRouteView } from "@/list/useRouteView";

import { DEFAULT_POSTER_OPTIONS, POSTER_GAP, POSTER_PADDING, POSTER_WIDTHS, PosterOptionsMenu, posterRowHeight, type PosterOptions } from "./IndexViews";
import { Poster } from "./Poster";

type Network = components["schemas"]["SeriesNetwork"];

function Logo({ network, size }: { network: Network; size: string }) {
  return network.logo_url ? (
    <img src={remoteImage(network.logo_url, "thumb")} alt="" loading="lazy" className={`${size} object-contain`} />
  ) : (
    <Radio aria-hidden className="size-8 text-ink-muted" />
  );
}

/** A TV library's Networks level: where its series air, most first, on the same wall as every other level. */
export function TVNetworks() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const [storedOptions, setOptions] = usePreference<PosterOptions>("posters.options", DEFAULT_POSTER_OPTIONS);
  const options = { ...DEFAULT_POSTER_OPTIONS, ...storedOptions };
  const frameRef = useRef<HTMLDivElement>(null);
  const metrics = useGridMetrics(frameRef, POSTER_WIDTHS[options.size], POSTER_GAP, POSTER_PADDING);
  const view = useRouteView([]);
  const networks = useQuery({
    queryKey: ["series-networks", categoryId],
    queryFn: async () => {
      const { data, error } = await api.GET("/series/networks", { params: { query: { category_id: Number(categoryId) } } });
      if (error) throw error;
      return data;
    },
  });
  const list = useLibraryList<Network>(
    {
      name: `series-networks-${categoryId}-${networks.dataUpdatedAt}`,
      idOf: (network) => network.id,
      rowHeight: posterRowHeight({ ...options, showAvailability: false }, metrics.cellWidth),
      itemsPerRow: metrics.columns,
      fetchPage: async () => ({ items: networks.data ?? [] }),
    },
    view,
  );

  return (
    <div ref={frameRef} className="flex h-full flex-col">
      <div className="flex shrink-0 items-center gap-3 border-b border-border-subtle px-4 py-2">
        <h2 className="text-sm font-medium">Networks</h2>
        <div className="ml-auto">
          <PosterOptionsMenu options={options} onChange={setOptions} />
        </div>
      </div>
      <div className="min-h-0 flex-1">
        {networks.isLoading && <p className="p-4 text-sm text-ink-muted">Loading…</p>}
        {networks.isError && <p className="p-4 text-sm text-danger">The networks could not be loaded.</p>}
        {networks.data && networks.data.length === 0 && <p className="p-4 text-sm text-ink-muted">No series with a known network yet.</p>}
        {list.items.length > 0 && (
          <PosterGrid
            list={list}
            idOf={(network) => network.id}
            renderPoster={(network) => (
              <Poster
                subject="works"
                imageId={null}
                imageSrc={network.logo_url ? remoteImage(network.logo_url, "small") : undefined}
                contain
                title={network.name}
                subtitle={`${network.series_count} series`}
                link={{ to: "/library/$categoryId/networks/$networkId", params: { categoryId, networkId: String(network.id) } }}
                showTitle={options.showTitle}
              />
            )}
          />
        )}
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

  const { network, series } = detail.data;
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
      <FollowPanel subject="organisation" subjectId={network.id} libraryIds={[Number(categoryId)]} />
      <div className="grid grid-cols-[repeat(auto-fill,minmax(9rem,1fr))] gap-4">
        {series.map((entry) => (
          <SeriesPosterTile key={entry.id} series={entry} categoryId={categoryId} />
        ))}
      </div>
    </div>
  );
}
