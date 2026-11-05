import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { Card } from "./SettingsCard";

type RequestView = components["schemas"]["RequestView"];

const STATE_LABEL: Record<RequestView["state"], string> = {
  PENDING: "Waiting for approval",
  APPROVED: "Approved",
  DECLINED: "Declined",
  FULFILLED: "Added",
};

/** What people asked to have added. An administrator sees everyone's and decides the waiting ones; anyone else sees
 *  their own and how each was answered. */
export function Requests() {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const requests = useQuery({
    queryKey: ["requests"],
    queryFn: () => ok(api.GET("/requests", { params: { query: { limit: 200 } } })),
  });
  const decided = () => void queryClient.invalidateQueries({ queryKey: ["requests"] });
  const approve = useMutation({
    mutationFn: (id: number) => ok(api.POST("/requests/{id}/approve", { params: { path: { id } } })),
    onSuccess: () => {
      decided();
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });
  const decline = useMutation({
    mutationFn: ({ id, reason }: { id: number; reason: string }) =>
      ok(api.POST("/requests/{id}/decline", { params: { path: { id } }, body: { reason } })),
    onSuccess: decided,
  });
  const [declining, setDeclining] = useState<number>();
  const [reason, setReason] = useState("");
  const items = requests.data?.items ?? [];
  const waiting = items.filter((entry) => entry.state === "PENDING");

  return (
    <Card
      title="Requests"
      help={
        administrator
          ? "Works people asked for. Approving adds the work to its library as they asked; declining tells them why."
          : "What you asked for, and whether it was added. Anything you request waits here until an administrator approves it."
      }
    >
      {requests.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {requests.error !== null && <p className="text-sm text-ink-muted">The requests could not be read.</p>}
      {requests.data && items.length === 0 && (
        <p className="text-sm text-ink-muted">Nothing has been requested. Use Request on a library&apos;s Add page.</p>
      )}
      {administrator && waiting.length > 0 && (
        <p className="text-xs text-ink-muted">{waiting.length} waiting for a decision.</p>
      )}
      <ul className="flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto">
        {items.map((entry) => (
          <li key={entry.id} className="flex flex-col gap-2 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm">
            <div className="flex items-center gap-3">
              <div className="min-w-0 flex-1">
                <div className="truncate font-medium">{entry.title}</div>
                <div className="truncate text-xs text-ink-muted">
                  {[
                    administrator ? `by ${entry.username}` : undefined,
                    entry.media_type,
                    new Date(entry.created_at).toLocaleDateString(),
                    STATE_LABEL[entry.state],
                    entry.decline_reason ? `“${entry.decline_reason}”` : undefined,
                  ]
                    .filter(Boolean)
                    .join(" · ")}
                </div>
              </div>
              {administrator && entry.state === "PENDING" && (
                <div className="flex shrink-0 gap-2">
                  <button
                    type="button"
                    disabled={approve.isPending}
                    onClick={() => approve.mutate(entry.id)}
                    className="rounded-md border border-border-subtle px-2 py-1 text-xs enabled:hover:text-accent disabled:opacity-50"
                  >
                    Approve
                  </button>
                  <button
                    type="button"
                    onClick={() => {
                      setDeclining(declining === entry.id ? undefined : entry.id);
                      setReason("");
                    }}
                    className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink"
                  >
                    Decline…
                  </button>
                </div>
              )}
            </div>
            {declining === entry.id && (
              <form
                className="flex items-center gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  decline.mutate({ id: entry.id, reason });
                  setDeclining(undefined);
                }}
              >
                <input
                  autoFocus
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  placeholder="Reason shown to the requester (optional)"
                  aria-label="Reason for declining"
                  className="min-w-0 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs"
                />
                <button type="submit" className="rounded-md border border-border-subtle px-2 py-1 text-xs">
                  Decline
                </button>
              </form>
            )}
          </li>
        ))}
      </ul>
      {(approve.isError || decline.isError) && (
        <p className="text-xs text-danger">{((approve.error ?? decline.error) as { detail?: string }).detail ?? "That could not be decided."}</p>
      )}
    </Card>
  );
}
