import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";

import { api } from "@/api/client";

import { useDebouncedValue } from "./useDebouncedValue";

/** "Identify as...": search the library's metadata provider and place a held unit as the album picked, for a unit
 *  nothing identified or one identified wrongly. */
export function IdentifyUnitAs({
  sessionId,
  unitId,
  categoryId,
  onClose,
  onDone,
}: {
  sessionId: number;
  unitId: number;
  categoryId: number;
  onClose: () => void;
  onDone: () => void;
}) {
  const [typed, setTyped] = useState("");
  const query = useDebouncedValue(typed.trim(), 300);
  const results = useQuery({
    queryKey: ["identify-as-search", categoryId, query],
    enabled: query.length >= 2,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/lookup", {
        params: { query: { category_id: categoryId, q: query, kind: "album", limit: 15 } },
      });
      if (error) throw error;
      return data;
    },
  });
  const place = useMutation({
    mutationFn: async (choice: { provider: string; external_id: string }) => {
      const { error } = await api.POST("/import/sessions/{id}/units/{unitId}/commit", {
        params: { path: { id: sessionId, unitId } },
        body: choice,
      });
      if (error) throw error;
    },
    onSuccess: onDone,
  });

  return (
    <Dialog.Root open onOpenChange={(next) => !next && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          className="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[min(36rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2
                     flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-4 shadow-xl"
        >
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">Identify as…</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-xs text-ink-muted">
            Search for the album these files are, then pick it: they are placed as that album, whatever identification made of them.
          </Dialog.Description>
          <input
            autoFocus
            value={typed}
            onChange={(event) => setTyped(event.target.value)}
            placeholder="Artist and album"
            aria-label="Search for the album"
            className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm"
          />
          {results.isLoading && <p className="text-xs text-ink-muted">Searching…</p>}
          {results.error !== null && !results.isLoading && <p className="text-xs text-danger">The search failed. Try again in a moment.</p>}
          {results.data && results.data.length === 0 && <p className="text-xs text-ink-muted">Nothing found.</p>}
          {place.error !== null && <p className="text-xs text-danger">That could not be placed. Pick another or try again.</p>}
          <ul className="flex min-h-0 flex-col gap-1 overflow-y-auto">
            {(results.data ?? []).map((result) => (
              <li key={`${result.provider}:${result.external_id}`}>
                <button
                  type="button"
                  disabled={place.isPending}
                  onClick={() => place.mutate({ provider: result.provider, external_id: result.external_id })}
                  className="flex w-full flex-col rounded-md border border-border-subtle px-3 py-2 text-left text-sm hover:border-accent disabled:opacity-50"
                >
                  <span className="font-medium">{result.title}</span>
                  <span className="text-xs text-ink-muted">
                    {[result.subtitle, result.year].filter(Boolean).join(" · ")}
                    {result.library_id != null ? " · already in the library" : ""}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
