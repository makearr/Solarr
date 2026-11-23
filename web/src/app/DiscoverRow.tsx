import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";
import { useDiscoverLibraries } from "@/lib/resources";

import { RecommendationCard } from "./Discover";
import { AddToLibrary } from "./work/AddToSolarr";

type Recommendation = components["schemas"]["Recommendation"];

/** One scrolling line of recommendations across the caller's libraries; nothing at all while there are none. */
export function DiscoverRow() {
  const queryClient = useQueryClient();
  const libraries = useDiscoverLibraries();
  const [adding, setAdding] = useState<Recommendation>();
  const row = useQuery({
    queryKey: ["discover-dashboard"],
    queryFn: () => ok(api.GET("/discover/dashboard", { params: { query: { limit: 20 } } })),
    enabled: (libraries.data?.length ?? 0) > 0,
    meta: { handlesErrors: true },
  });
  const dismiss = useMutation({
    meta: { handlesErrors: true },
    mutationFn: (id: number) => ok(api.POST("/discover/recommendations/{id}/dismiss", { params: { path: { id } } })),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["discover-dashboard"] });
      void queryClient.invalidateQueries({ queryKey: ["discover-recommendations"] });
    },
  });
  if (!row.data || row.data.length === 0) return null;
  const canAdd = (item: Recommendation) => libraries.data?.find((library) => library.category_id === item.category_id)?.can_add ?? false;
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-medium">{t("discover.page")}</h2>
        <Link to="/discover" className="text-xs text-accent">{t("discover.seeAll")}</Link>
      </div>
      <ul className="flex gap-4 overflow-x-auto pb-2">
        {row.data.map((item) => (
          <li key={item.id} className="w-36 shrink-0">
            <RecommendationCard item={item} canAdd={canAdd(item)} onAdd={() => setAdding(item)} onDismiss={() => dismiss.mutate(item.id)} />
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
