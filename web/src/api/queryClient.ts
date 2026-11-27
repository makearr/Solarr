import { MutationCache, QueryClient } from "@tanstack/react-query";

import { t } from "@/lib/i18n";
import { showToast } from "@/lib/toast";
import { problemText, pushClientError } from "@/lib/useNotifications";

// Server state lives in TanStack Query and nowhere else (ADR-31); one client, shared with api/client.ts's 401 hook.
export const queryClient = new QueryClient({
  // A mutation that renders its own error says so with `meta: { handlesErrors: true }` or an onError; the rest are told to the bar.
  // A mutation names what it did in `meta: { success: "..." }` and gets a toast; its failure is a toast with the server's detail.
  mutationCache: new MutationCache({
    onSuccess: (_data, _variables, _context, mutation) => {
      const message = mutation.meta?.success;
      if (typeof message === "string") showToast({ message });
    },
    onError: (error, _variables, _context, mutation) => {
      if (mutation.meta?.handlesErrors === true || mutation.options.onError !== undefined) return;
      const detail = problemText(error);
      pushClientError(detail);
      showToast({ tone: "error", message: t("toast.failed"), detail });
    },
  }),
  defaultOptions: {
    queries: {
      // Invalidation is SSE-driven, not polled.
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      retry: (failureCount, error) => {
        // These never succeed on retry.
        const status = (error as { status?: number }).status;
        if (status === 401 || status === 403 || status === 404) return false;
        return failureCount < 2;
      },
    },
  },
});
