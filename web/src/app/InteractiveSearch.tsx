import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { t } from "@/lib/i18n";
import { Dialog } from "@/ui/Dialog";

import { CandidateList } from "./CandidateList";
import { IndexerProblems } from "./IndexerProblems";

/** What of a work a search is for: one group by its ref (a season), one part by its id, or the work's own item. */
export interface SearchScope {
  group?: string;
  part?: number;
}

/** Lidarr's and Sonarr's Interactive Search: search now, list what was found best first, the person sends one. */
export function InteractiveSearch({
  workId,
  scope = {},
  title,
  onClose,
}: {
  workId: number;
  scope?: SearchScope;
  title: string;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const query = { ...(scope.group !== undefined ? { group: scope.group } : {}), ...(scope.part ? { part: scope.part } : {}) };
  // A query, not a mutation: opening the dialog is the search, and reopening reuses it for a minute.
  const found = useQuery({
    queryKey: ["work", workId, "interactive-search", query],
    queryFn: async () => {
      const { data, error } = await api.POST("/works/{id}/search", { params: { path: { id: workId }, query } });
      if (error) throw error;
      return data;
    },
    staleTime: 60_000,
    retry: false,
  });
  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      const { error } = await api.POST("/works/{id}/candidates/{decisionId}/send", {
        params: { path: { id: workId, decisionId }, query },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["wanted"] });
      void queryClient.invalidateQueries({ queryKey: ["series"] });
      void queryClient.invalidateQueries({ queryKey: ["work", workId] });
    },
  });

  return (
    <Dialog
      width="wide"
      title={`${t("action.interactive")}: ${title}`}
      description="Best first. A release that covers more of what is missing leads at equal quality. Send puts it in the download client's queue."
      onClose={onClose}
    >
      <IndexerProblems problems={found.data?.indexer_errors} />
      {found.isError ? (
        <p className="text-xs text-danger">{t("search.failed")}</p>
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
      {send.isSuccess && <p className="mt-2 text-xs text-ok">{t("grab.sent")}.</p>}
    </Dialog>
  );
}
