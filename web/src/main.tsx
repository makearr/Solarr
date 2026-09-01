import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { App } from "./app/App";
import "./index.css";

// Server state lives in TanStack Query and nowhere else (ADR-31). Nothing here
// is mirrored into component state, because two copies of the same data is how
// a list and its detail panel end up disagreeing.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Invalidation is driven by SSE, not by polling. A refetch on every window
      // focus would multiply requests across a library of 500k items for data
      // the event stream has already told us is unchanged.
      refetchOnWindowFocus: false,
      staleTime: 30_000,
      retry: (failureCount, error) => {
        // Authentication and authorisation failures never succeed on retry;
        // retrying them only delays showing the user the login form.
        const status = (error as { status?: number }).status;
        if (status === 401 || status === 403 || status === 404) return false;
        return failureCount < 2;
      },
    },
  },
});

const container = document.getElementById("root");
if (!container) throw new Error("index.html is missing #root");

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>,
);
