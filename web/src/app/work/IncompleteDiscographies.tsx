import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { invalidatePeopleLists } from "@/lib/resources";

/** Library-derived discovery: artists the library holds an album of whose discography has albums it does not. */
export function IncompleteDiscographies({ categoryId }: { categoryId: number }) {
  const queryClient = useQueryClient();
  const artists = useQuery({
    queryKey: ["discover-incomplete", categoryId],
    queryFn: () => ok(api.GET("/discover/incomplete", { params: { query: { category_id: categoryId, limit: 30 } } })),
  });
  const follow = useMutation({
    mutationFn: async (personId: number) => {
      await ok(api.PUT("/people/{id}/follow", {
        params: { path: { id: personId } },
        body: { category_id: categoryId, search: true },
      }));
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["discover-incomplete", categoryId] });
      invalidatePeopleLists(queryClient);
    },
  });

  return (
    <section className="flex min-h-0 flex-col gap-2 overflow-y-auto">
      <h3 className="text-sm font-medium">Artists you have albums of, but not all</h3>
      <p className="text-xs text-ink-muted">
        From your library, compared with each artist's discography (no live albums or compilations). Updated weekly.
      </p>
      {artists.isLoading && <p className="text-sm text-ink-muted">Reading…</p>}
      {artists.data && artists.data.length === 0 && (
        <p className="text-sm text-ink-muted">Nothing yet. This fills in after the weekly comparison has run (Settings → Scheduler → Compare discographies).</p>
      )}
      <ul className="flex flex-col gap-1">
        {artists.data?.map((artist) => (
          <li
            key={artist.person_id}
            className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm"
          >
            <Link
              to="/people/$id"
              params={{ id: String(artist.person_id) }}
              className="min-w-0 flex-1 truncate font-medium hover:underline"
            >
              {artist.name}
            </Link>
            <span className="shrink-0 text-xs tabular-nums text-ink-muted">
              {artist.held} of {artist.total} albums · {artist.total - artist.held} missing
            </span>
            {artist.monitored ? (
              <span className="shrink-0 text-xs text-ink-muted">Followed</span>
            ) : (
              <button
                type="button"
                disabled={follow.isPending}
                onClick={() => follow.mutate(artist.person_id)}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink disabled:opacity-50"
              >
                Monitor and add the rest
              </button>
            )}
          </li>
        ))}
      </ul>
      {follow.isError && <p className="text-xs text-danger">That could not be started.</p>}
    </section>
  );
}
