import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, type MessageKey } from "@/lib/i18n";
import { remoteImage } from "@/lib/remoteImage";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaType } from "@/lib/useMediaTypes";
import { Chip } from "@/ui/Chip";

type Preview = components["schemas"]["WorkPreview"];

/** What the providers describe of an item the library lacks; undefined while it loads, and when the type cannot describe. */
export function useWorkPreview(categoryId: string, provider: string, externalId: string, kind?: string) {
  return useQuery({
    queryKey: ["work-preview", Number(categoryId), provider, externalId, kind ?? ""],
    retry: false,
    staleTime: 10 * 60 * 1000,
    queryFn: () => ok(api.GET("/works/describe", { params: { query: { category_id: Number(categoryId), provider, external_id: externalId, kind } } })),
  });
}

const FACT_LABELS: Record<string, MessageKey> = {
  network: "external.fact.network",
  studio: "external.fact.studio",
  runtime_min: "external.fact.runtime_min",
  certification: "external.fact.certification",
  language: "external.fact.language",
  first_aired: "external.fact.first_aired",
  released: "external.fact.released",
  publisher: "external.fact.publisher",
  author: "external.fact.author",
  collection: "external.fact.collection",
  website: "external.fact.website",
};

const factLabel = (key: string) => {
  const label = FACT_LABELS[key];
  return label ? t(label) : key;
};

const PART_ROW_HEIGHT = 32;
const PARTS_VIEWPORT_ROWS = 12;

function duration(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

/** The provider's facts, ratings, people and parts of an item the library lacks, laid out as the library's own item page does. */
export function ExternalPreview({ preview, categoryId }: { preview: Preview; categoryId: string }) {
  const mediaType = useMediaType(useLibrary(categoryId)?.media_type);
  const roleLabel = (key: string) => mediaType?.roles.find((role) => role.key === key)?.label ?? key;
  return (
    <div className="flex flex-col gap-5">
      {preview.facts.length > 0 && (
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          {preview.facts.map((fact) => (
            <div key={`${fact.key}-${fact.value}`} className="contents">
              <dt className="text-ink-muted">{factLabel(fact.key)}</dt>
              <dd className="break-words">{fact.key === "runtime_min" ? t("external.minutes", { count: fact.value }) : fact.value}</dd>
            </div>
          ))}
        </dl>
      )}
      {(preview.genres.length > 0 || preview.ratings.length > 0) && (
        <div className="flex flex-wrap items-center gap-2">
          {preview.genres.map((genre) => (
            <Chip key={genre}>{genre}</Chip>
          ))}
          {preview.ratings.map((rating) => (
            <Chip key={rating.provider} tone="accent" title={t("external.ratings")}>
              {rating.provider} {rating.score.toFixed(1)}
            </Chip>
          ))}
        </div>
      )}
      {preview.people.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-sm font-semibold">{t("external.cast")}</h2>
          <ul className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
            {preview.people.map((person, index) => (
              <li key={`${person.name}-${person.role}-${index}`} className="flex items-center gap-2 text-xs">
                {person.image_url ? (
                  <img src={remoteImage(person.image_url, "small")} alt="" className="size-10 shrink-0 rounded-full border border-border-subtle object-cover" />
                ) : (
                  <span className="flex size-10 shrink-0 items-center justify-center rounded-full border border-border-subtle bg-surface text-ink-muted">{person.name.charAt(0)}</span>
                )}
                <span className="min-w-0">
                  <span className="block truncate font-medium">{person.name}</span>
                  <span className="block truncate text-ink-muted">{person.character || roleLabel(person.role)}</span>
                </span>
              </li>
            ))}
          </ul>
        </section>
      )}
      {preview.groups.length > 0 && (
        <section className="flex flex-col gap-3">
          <h2 className="text-sm font-semibold">{t("external.parts")}</h2>
          {preview.groups.map((group) => (
            <PreviewGroup key={group.number} group={group} groupName={mediaType?.group.one || ""} />
          ))}
        </section>
      )}
    </div>
  );
}

function PreviewGroup({ group, groupName }: { group: Preview["groups"][number]; groupName: string }) {
  const scroller = useRef<HTMLDivElement>(null);
  const rows = useVirtualizer({ count: group.parts.length, getScrollElement: () => scroller.current, estimateSize: () => PART_ROW_HEIGHT, overscan: 8 });
  const height = Math.min(group.parts.length, PARTS_VIEWPORT_ROWS) * PART_ROW_HEIGHT;
  return (
    <div className="flex flex-col gap-1">
      <h3 className="text-xs font-semibold text-ink-muted">{group.title || t("external.group", { group: groupName, number: group.number }).trim()}</h3>
      <div ref={scroller} className="overflow-auto rounded-md border border-border-subtle text-sm" style={{ height }}>
        <ol className="relative" style={{ height: rows.getTotalSize() }}>
          {rows.getVirtualItems().map((row) => {
            const part = group.parts[row.index];
            if (!part) return null;
            return (
              <li
                key={row.index}
                className="absolute inset-x-0 flex items-center gap-3 border-b border-border-subtle px-3"
                style={{ top: 0, height: PART_ROW_HEIGHT, transform: `translateY(${row.start}px)` }}
              >
                <span className="w-10 shrink-0 text-right tabular-nums text-ink-muted">{part.number}</span>
                <span className="min-w-0 flex-1 truncate">{part.title}</span>
                {part.date && <span className="shrink-0 text-xs text-ink-muted">{part.date}</span>}
                {part.duration_sec ? <span className="w-12 shrink-0 text-right text-xs tabular-nums text-ink-muted">{duration(part.duration_sec)}</span> : null}
              </li>
            );
          })}
        </ol>
      </div>
    </div>
  );
}
