import { QueryClient } from "@tanstack/react-query";

// Server state lives in TanStack Query and nowhere else (ADR-31); one client, shared with api/client.ts's 401 hook.
export const queryClient = new QueryClient({
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
