import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useParams, useRouter, useSearch } from "@tanstack/react-router";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { Button } from "@/ui/Button";
import { Chip } from "@/ui/Chip";
import { IgnoreButton } from "@/ui/IgnoreButton";

import { AddToLibrary } from "./work/AddToSolarr";

interface ExternalSearch {
  kind?: string;
  title?: string;
}

/** Where a card for an item the library lacks leads: the item as its provider describes it, with Add and Ignore. One page for every media type. */
export function externalItemLink(categoryId: string | number, provider: string, externalId: string, title: string, kind?: string) {
  return {
    to: "/library/$categoryId/external/$provider/$externalId",
    params: { categoryId: String(categoryId), provider, externalId },
    search: { title, kind },
  } as const;
}

/** The title of an item the library lacks, as a link to its page; plain text where no router is mounted (a card shown alone). */
export function ExternalTitle({ categoryId, provider, externalId, title, kind }: { categoryId: string | number; provider: string; externalId: string; title: string; kind?: string }) {
  const router = useRouter({ warn: false }) as ReturnType<typeof useRouter> | undefined;
  if (!router) return <>{title}</>;
  return (
    <Link {...externalItemLink(categoryId, provider, externalId, title, kind)} className="hover:underline">
      {title}
    </Link>
  );
}

export function ExternalItem() {
  const { categoryId, provider, externalId } = useParams({ strict: false }) as { categoryId: string; provider: string; externalId: string };
  const search = useSearch({ strict: false }) as ExternalSearch;
  const library = useLibrary(categoryId);
  const [adding, setAdding] = useState(false);
  const title = search.title ?? "";
  const found = useQuery({
    queryKey: ["work-lookup", Number(categoryId), search.kind ?? "", title, "external"],
    enabled: title !== "",
    queryFn: () => ok(api.GET("/works/lookup", { params: { query: { category_id: Number(categoryId), q: title, kind: search.kind, limit: 30 } } })),
  });
  const item = found.data?.find((candidate) => candidate.provider === provider && candidate.external_id === externalId);
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4 p-6">
      <Link to="/library/$categoryId/discover" params={{ categoryId }} className="w-fit text-xs text-ink-muted hover:text-ink">
        {library?.name ?? ""}
      </Link>
      <div className="flex flex-col gap-4 sm:flex-row">
        <div className="w-40 shrink-0">
          {item?.poster_url ? (
            <img src={remoteImage(item.poster_url, "small")} alt="" className="aspect-[2/3] w-full rounded-lg border border-border-subtle object-cover" />
          ) : (
            <div className="flex aspect-[2/3] w-full items-center justify-center rounded-lg border border-border-subtle bg-surface text-3xl text-ink-muted">
              {title.trim().charAt(0).toUpperCase() || "?"}
            </div>
          )}
        </div>
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <h1 className="text-xl font-semibold">
            {item?.title ?? title}
            {item?.year ? <span className="font-normal text-ink-muted"> ({item.year})</span> : null}
          </h1>
          {item?.subtitle && <p className="text-sm text-ink-muted">{item.subtitle}</p>}
          <div className="flex flex-wrap items-center gap-2">
            {item?.status && <Chip>{item.status}</Chip>}
            <Chip title={t("external.providerHelp")}>{provider}</Chip>
            {item?.ignored && <Chip>{t("ignore.ignored")}</Chip>}
          </div>
          {found.isPending && title !== "" && <p className="text-sm text-ink-muted">{t("external.loading")}</p>}
          {item?.overview && <p className="max-w-prose text-sm leading-relaxed">{item.overview}</p>}
          {found.data && !item && <p className="text-sm text-ink-muted">{t("external.notFound")}</p>}
          <div className="mt-2 flex flex-wrap items-center gap-2">
            {item?.library_id ? (
              <Link to="/works/$id" params={{ id: String(item.library_id) }} className="text-sm text-ok">
                {t("discover.inLibrary")}
              </Link>
            ) : (
              <Button variant="primary" onClick={() => setAdding(true)}>
                {t("discover.add")}
              </Button>
            )}
            {!item?.library_id && !item?.ignored && <IgnoreButton item={{ categoryId: Number(categoryId), provider, externalId, title: item?.title ?? title }} />}
          </div>
        </div>
      </div>
      {adding && (
        <AddToLibrary
          candidate={{ provider, external_id: externalId, title: item?.title ?? title, poster_url: item?.poster_url, year: item?.year, kind: item?.kind ?? search.kind }}
          categoryId={categoryId}
          onDone={() => setAdding(false)}
        />
      )}
    </div>
  );
}
