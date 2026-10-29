import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { EmptyState } from "@/ui/EmptyState";
import { PageHeader } from "@/ui/PageHeader";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaType } from "@/lib/useMediaTypes";
import { useMetadataSettings } from "@/lib/resources";
import { OrderedSources, usePatchMetadataSettings } from "./MetadataSettings";

type Sources = components["schemas"]["LibraryMetadataSources"];

/** The metadata providers one library asks, in order: the first that holds a field supplies it, each later one only fills
 *  what the earlier ones left empty, and one left out is not asked. */
export function LibraryMetadata() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const id = Number(categoryId);
  const library = useLibrary(categoryId);
  const mediaType = useMediaType(library?.media_type);
  const queryClient = useQueryClient();
  const key = ["libraryMetadataSources", id];
  const saved = useQuery({
    queryKey: key,
    queryFn: () => ok(api.GET("/categories/{id}/metadata-sources", { params: { path: { id } } })),
  });
  const save = useMutation({
    mutationFn: (sources: string[]) => ok(api.PUT("/categories/{id}/metadata-sources", { params: { path: { id } }, body: { sources } })),
    onSuccess: (data: Sources) => queryClient.setQueryData(key, data),
  });
  if (saved.isLoading) return <p className="p-6 text-sm text-ink-muted">Loading…</p>;
  if (!saved.data) return <p className="p-6 text-sm text-danger">The providers could not be read.</p>;
  const { sources, available, custom } = saved.data;
  if (available.length === 0) return <EmptyState title="This kind of library has no metadata providers to choose from." />;
  const labelOf = (provider: string) => available.find((entry) => entry.key === provider);
  const unused = available.filter((provider) => !sources.includes(provider.key));
  const move = (index: number, delta: number) => {
    const next = [...sources];
    const other = next[index + delta];
    const moving = next[index];
    if (other === undefined || moving === undefined) return;
    next[index + delta] = moving;
    next[index] = other;
    save.mutate(next);
  };
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-4 p-4">
        <PageHeader
          title="Metadata providers"
          subtitle="Where this library's titles, descriptions, people and pictures come from. The first provider that has a field supplies it; each one after it only fills in what the ones before left empty. A provider left out is not asked."
        />
        <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
          <div className="flex items-center gap-2">
            <h2 className="text-sm font-semibold">Order</h2>
            <Chip>{custom ? "this library's own" : "the default for its kind"}</Chip>
            {custom && (
              <Button className="ml-auto" busy={save.isPending} onClick={() => save.mutate([])}>
                Use the default order
              </Button>
            )}
          </div>
          <ol className="flex flex-col divide-y divide-border-subtle text-sm">
            {sources.map((provider, index) => {
              const info = labelOf(provider);
              return (
                <li key={provider} className="flex flex-wrap items-center gap-2 py-2">
                  <span className="w-5 text-right tabular-nums text-ink-muted">{index + 1}.</span>
                  <span className="font-medium">{info?.label ?? provider}</span>
                  {info?.needs_key && <Chip tone="warn">needs your key</Chip>}
                  <span className="min-w-0 flex-1 text-xs text-ink-muted">{info?.carries.join(", ")}</span>
                  <Button variant="icon" aria-label={`Move ${info?.label ?? provider} up`} disabled={index === 0 || save.isPending} onClick={() => move(index, -1)}>
                    <ArrowUp aria-hidden className="size-4" />
                  </Button>
                  <Button variant="icon" aria-label={`Move ${info?.label ?? provider} down`} disabled={index === sources.length - 1 || save.isPending} onClick={() => move(index, 1)}>
                    <ArrowDown aria-hidden className="size-4" />
                  </Button>
                  <Button disabled={sources.length === 1 || save.isPending} onClick={() => save.mutate(sources.filter((entry) => entry !== provider))}>
                    Stop using
                  </Button>
                </li>
              );
            })}
          </ol>
          {unused.length > 0 && (
            <div className="flex flex-col gap-1 border-t border-border-subtle pt-2 text-sm">
              <span className="text-xs text-ink-muted">Not used by this library</span>
              {unused.map((provider) => (
                <div key={provider.key} className="flex flex-wrap items-center gap-2">
                  <span>{provider.label}</span>
                  <span className="min-w-0 flex-1 text-xs text-ink-muted">{provider.carries.join(", ")}</span>
                  {provider.enabled ? (
                    <Button busy={save.isPending} onClick={() => save.mutate([...sources, provider.key])}>
                      Use last
                    </Button>
                  ) : (
                    <Chip tone="warn">switched off under Settings, Metadata</Chip>
                  )}
                </div>
              ))}
            </div>
          )}
          {save.isError && <p className="text-xs text-danger">That could not be saved.</p>}
        </section>
        {mediaType?.cover_sources && <CoverOrder />}
        <p className="text-xs text-ink-muted">
          A library starts with the providers that suit its kind, Wikidata first: the ids its item records steer the others to the right
          entry. Providers are switched on and off, and their keys given, under Settings, Metadata. Each library is looked up in its own
          order; an album or artist two music libraries hold follows the first one&apos;s.
        </p>
      </div>
    </div>
  );
}

const COVER_SOURCES = [
  { id: "coverartarchive", label: "Cover Art Archive", note: "MusicBrainz's cover archive, fetched by release and then by release group." },
  { id: "embedded", label: "Embedded art", note: "The picture already inside your files, used where nothing earlier found a cover." },
  { id: "theaudiodb", label: "TheAudioDB", note: "Album covers from theaudiodb.com, by release group, where nothing earlier found one." },
] as const;

/** Where album covers come from, in order; one for every music library. */
function CoverOrder() {
  const metadata = useMetadataSettings();
  const patch = usePatchMetadataSettings(metadata.data);
  if (!metadata.data) return null;
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <h2 className="text-sm font-semibold">Covers</h2>
      <OrderedSources
        title="Each source in turn; a later one only fills what the earlier ones left missing. One order for every music library."
        sources={COVER_SOURCES}
        order={metadata.data.cover_sources ?? []}
        busy={patch.isPending}
        onChange={(order) => patch.mutate({ cover_sources: order })}
      />
      {patch.isError && <p className="text-xs text-danger">That could not be saved.</p>}
    </section>
  );
}
