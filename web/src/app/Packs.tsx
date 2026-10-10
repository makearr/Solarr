import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Pack = components["schemas"]["Pack"];
type PackItem = components["schemas"]["PackItem"];

/** ADR-50, todo 4.11: one source, many works -- a discography, a box set, an
 *  anthology or a compilation, detected structurally (and confirmed by name)
 *  once a non-adoption import session finishes walking. A provenance fact,
 *  not a library level of its own -- every member is a real work, browsable
 *  in its own library the moment it resolves. */
export function Packs() {
  const [expandedId, setExpandedId] = useState<number | null>(null);

  const packs = useQuery({
    queryKey: ["packs"],
    queryFn: async () => {
      const { data, error } = await api.GET("/packs", { params: { query: { limit: 100 } } });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto p-4">
      <div className="flex items-center gap-3">
        <h1 className="text-sm font-medium">Packs</h1>
        <span className="text-xs text-ink-muted">
          One source, many works -- a discography, box set, anthology or compilation
        </span>
      </div>

      {packs.isLoading && <Message>Loading packs…</Message>}
      {packs.error !== null && <Message>Packs could not be read.</Message>}
      {packs.data && packs.data.length === 0 && (
        <Message>
          No pack has been detected yet -- point Import at a source with several sibling releases
          by the same artist to see one here.
        </Message>
      )}
      {packs.data && packs.data.length > 0 && (
        <ul className="flex flex-col gap-2">
          {packs.data.map((pack) => (
            <PackRow
              key={pack.id}
              pack={pack}
              expanded={expandedId === pack.id}
              onToggle={() => setExpandedId((current) => (current === pack.id ? null : pack.id))}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function PackRow({ pack, expanded, onToggle }: { pack: Pack; expanded: boolean; onToggle: () => void }) {
  return (
    <li className="rounded-lg border border-border-subtle bg-surface">
      <button
        type="button"
        onClick={onToggle}
        className="flex w-full flex-wrap items-center gap-2 px-3 py-2.5 text-left text-xs
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <span className="font-mono text-sm text-ink">{pack.title}</span>
        <KindBadge kind={pack.kind} />
        <span className="text-ink-muted">
          {pack.resolved_count} of {pack.member_count} resolved
        </span>
        <span className="text-ink-muted">{Math.round(pack.confidence * 100)}% confidence</span>
        <span className="text-ink-muted">
          {pack.detected_from === "structure_and_name" ? "structure + name" : "structure"}
        </span>
      </button>
      {expanded && <PackItems packId={pack.id} />}
    </li>
  );
}

function PackItems({ packId }: { packId: number }) {
  const detail = useQuery({
    queryKey: ["pack", packId],
    queryFn: async () => {
      const { data, error } = await api.GET("/packs/{id}", { params: { path: { id: packId } } });
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="border-t border-border-subtle">
      {detail.isLoading && <Message>Loading members…</Message>}
      {detail.error !== null && <Message>Members could not be read.</Message>}
      {detail.data && (
        <ul className="divide-y divide-border-subtle">
          {detail.data.items.map((item) => (
            <PackItemRow key={item.ord} item={item} />
          ))}
        </ul>
      )}
    </div>
  );
}

function PackItemRow({ item }: { item: PackItem }) {
  return (
    <li className="flex items-center gap-2 px-3 py-2 text-xs">
      <StateBadge state={item.state} />
      {item.work_id !== undefined ? (
        <Link
          to="/works/$id"
          params={{ id: String(item.work_id) }}
          className="text-accent hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Work #{item.work_id}
        </Link>
      ) : (
        <span className="text-ink-muted">Not yet resolved</span>
      )}
    </li>
  );
}

function KindBadge({ kind }: { kind: Pack["kind"] }) {
  return (
    <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
      {kind}
    </span>
  );
}

function StateBadge({ state }: { state: PackItem["state"] }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "RESOLVED"
          ? "border-accent text-accent"
          : state === "FAILED"
            ? "border-danger text-danger"
            : "border-border-subtle text-ink-muted"
      }`}
    >
      {state}
    </span>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-3 text-xs text-ink-muted">{children}</p>;
}
