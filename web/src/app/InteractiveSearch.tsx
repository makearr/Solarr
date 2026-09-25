import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { X } from "lucide-react";

import { api } from "@/api/client";

import { CandidateList } from "./CandidateList";

/** Lidarr's Interactive Search: search now, list what was found best first, the person sends one. */
export function InteractiveSearch({
  workId,
  title,
  onClose,
}: {
  workId: number;
  title: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  // A query, not a mutation: opening the dialog is the search, and reopening reuses it for a minute.
  const found = useQuery({
    queryKey: ["work", workId, "interactive-search"],
    queryFn: async () => {
      const { data, error } = await api.POST("/works/{id}/search", { params: { path: { id: workId } } });
      if (error) throw error;
      return data;
    },
    staleTime: 60_000,
    retry: false,
  });
  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      const { error } = await api.POST("/works/{id}/candidates/{decisionId}/send", {
        params: { path: { id: workId, decisionId } },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["wanted"] }),
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
            Best first. Send puts a release in the download client's queue.
          </Dialog.Description>
          <div className="min-h-0 overflow-y-auto">
            {found.isError ? (
              <p className="text-xs text-danger">The search could not run -- is an indexer connected and enabled?</p>
            ) : (
              <CandidateList
                decisions={found.data?.decisions ?? []}
                loading={found.isLoading}
                onSend={(decisionId) => send.mutate(decisionId)}
                sendPendingId={send.isPending ? send.variables : undefined}
                sendError={send.isError ? send.variables : undefined}
              />
            )}
            {send.isSuccess && <p className="mt-2 text-xs text-ok">Sent to the download client.</p>}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
