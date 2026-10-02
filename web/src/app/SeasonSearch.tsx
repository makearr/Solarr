import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";

import { api } from "@/api/client";

import { CandidateList } from "./CandidateList";
import { IndexerProblems } from "./IndexerProblems";

/** Sonarr's interactive search for one season: search now, list what was found best first, the person sends one. */
export function SeasonSearchDialog({
  categoryId,
  seriesId,
  season,
  title,
  onClose,
}: {
  categoryId: number;
  seriesId: number;
  season: number;
  title: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const params = { path: { id: seriesId, season }, query: { category_id: categoryId } };
  const found = useQuery({
    queryKey: ["series", String(categoryId), String(seriesId), "season", season, "interactive-search"],
    queryFn: async () => {
      const { data, error } = await api.POST("/series/{id}/seasons/{season}/search", { params });
      if (error) throw error;
      return data;
    },
    staleTime: 60_000,
    retry: false,
  });
  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      const { error } = await api.POST("/series/{id}/seasons/{season}/candidates/{decision_id}/send", {
        params: { path: { ...params.path, decision_id: decisionId }, query: params.query },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["series", String(categoryId)] }),
  });

  return (
    <Dialog.Root open onOpenChange={(open) => !open && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          className="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[min(56rem,calc(100vw-2rem))]
                     -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-lg border border-border-subtle
                     bg-canvas p-4 shadow-xl"
        >
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">Interactive search: {title}</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-xs text-ink-muted">
            Best first. A season pack that covers more of what is missing leads at equal quality. Send puts a release in the
            download client's queue.
          </Dialog.Description>
          <IndexerProblems problems={found.data?.indexer_errors} />
          <div className="min-h-0 overflow-y-auto">
            {found.isError ? (
              <p className="text-xs text-danger">
                The search could not run: is an indexer connected and enabled, and is anything in this season missing and aired?
              </p>
            ) : (
              <CandidateList
                decisions={found.data?.decisions ?? []}
                loading={found.isLoading}
                onSend={(decisionId) => send.mutate(decisionId)}
                sendPendingId={send.isPending ? send.variables : undefined}
                sendError={send.isError ? send.variables : undefined}
                sendErrorDetail={send.isError ? (send.error as { detail?: string }).detail : undefined}
              />
            )}
            {send.isSuccess && <p className="mt-2 text-xs text-ok">Sent to the download client.</p>}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
