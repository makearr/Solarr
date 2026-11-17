import { MutationCache, QueryClient } from "@tanstack/react-query";

import { problemText, pushClientError } from "@/lib/useNotifications";

// Server state lives in TanStack Query and nowhere else (ADR-31); one client, shared with api/client.ts's 401 hook.
export const queryClient = new QueryClient({
  // A mutation that renders its own error says so with `meta: { handlesErrors: true }` or an onError; the rest are told to the bar.
  mutationCache: new MutationCache({
    onError: (error, _variables, _context, mutation) => {
      if (mutation.meta?.handlesErrors === true || mutation.options.onError !== undefined) return;
      pushClientError(problemText(error));
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
