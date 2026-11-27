import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { useDiscoverLibraries } from "@/lib/resources";
import { useGridColumns } from "@/lib/useGridColumns";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { RecommendationCard } from "./Discover";
import { useRecommendationDismissal } from "./recommendationActions";
import { AddToLibrary } from "./work/AddToSolarr";

type Recommendation = components["schemas"]["Recommendation"];

/** Cards are at least this wide; the dashboard shows exactly two rows of them, never a sideways scroll. */
const CARD_MIN = 144;
const GAP = 16;
const ROWS = 2;
/** Enough for two rows on the widest screens; the grid clips to what fits. */
const FETCH = 60;

/** Two rows of recommendations across the caller's libraries; nothing at all while there are none. */
export function DiscoverRow() {
  const queryClient = useQueryClient();
  const libraries = useDiscoverLibraries();
  const [adding, setAdding] = useState<Recommendation>();
  const row = useQuery({
    queryKey: ["discover-dashboard"],
    queryFn: () => ok(api.GET("/discover/dashboard", { params: { query: { limit: FETCH } } })),
    enabled: (libraries.data?.length ?? 0) > 0,
    meta: { handlesErrors: true },
  });
  const { dismiss } = useRecommendationDismissal();
  const { ref, columns } = useGridColumns(CARD_MIN, GAP);
  const administrator = useIsAdministrator();
  // Discover is on but nothing shows: say why instead of leaving the dashboard silent.
  if (libraries.data && libraries.data.length === 0) {
    return administrator ? <p className="text-xs text-ink-muted">{t("discover.noLibrariesOn")} <Link to="/settings/discover" className="text-accent">{t("discover.openSettings")}</Link></p> : null;
  }
  if (row.data && row.data.length === 0) {
    return <p className="text-xs text-ink-muted">{t("discover.nothingYet")} {administrator && <Link to="/settings/discover" className="text-accent">{t("discover.openSettings")}</Link>}</p>;
  }
  if (!row.data) return null;
  const libraryOf = (item: Recommendation) => libraries.data?.find((library) => library.category_id === item.category_id);
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-medium">{t("discover.page")}</h2>
        <Link to="/discover" className="text-xs text-accent">{t("discover.seeAll")}</Link>
      </div>
      <ul ref={ref} className="grid gap-4" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
        {row.data.slice(0, columns * ROWS).map((item) => (
          <li key={item.id} className="min-w-0">
            <RecommendationCard
              item={item}
              libraryName={libraryOf(item)?.name ?? ""}
              canAdd={libraryOf(item)?.can_add ?? false}
              onAdd={() => setAdding(item)}
              onDismiss={() => dismiss(item)}
            />
          </li>
        ))}
      </ul>
      {adding && (
        <AddToLibrary
          candidate={{ provider: adding.provider, external_id: adding.external_id, title: adding.title, poster_url: adding.poster_url, year: adding.year, kind: adding.kind }}
          categoryId={String(adding.category_id)}
          onDone={() => {
            setAdding(undefined);
            void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
          }}
        />
      )}
    </section>
  );
}
