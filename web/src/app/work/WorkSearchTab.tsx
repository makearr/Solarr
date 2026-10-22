import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t, type Vocabulary } from "@/lib/i18n";
import { useIsAdministrator } from "@/lib/useAdministrator";
import type { MediaTypeInfo } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";

import { CandidateList } from "../CandidateList";
import { IndexerProblems } from "../IndexerProblems";
import { grabNote } from "./grabNote";
import { relativeTime } from "./workFormat";

type Work = components["schemas"]["WorkDetail"];
type Wanted = components["schemas"]["Wanted"];

/** The work's search: what is searched (the work, or one group), when it was and will be searched, Search and
 *  Interactive search, different terms where the type takes them, and the latest search's releases. */
export function WorkSearchTab({ work, info, words, lead }: { work: Work; info: MediaTypeInfo | undefined; words: Vocabulary; lead: string }) {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const groups = work.groups ?? [];
  // The work's own item, else the newest monitored group, as the Overview opens the newest season.
  const [group, setGroup] = useState(
    groups.find((entry) => entry.ref === "")?.ref ?? [...groups].reverse().find((entry) => entry.monitored)?.ref ?? groups[groups.length - 1]?.ref,
  );
  const query = group ? { group } : {};
  const selected = groups.find((entry) => entry.ref === group);

  const candidates = useQuery({
    queryKey: ["work", work.id, "candidates", query],
    enabled: group !== undefined,
    queryFn: () => ok(api.GET("/works/{id}/candidates", { params: { path: { id: work.id }, query } })),
  });
  const changed = () => void queryClient.invalidateQueries({ queryKey: ["work", work.id] });
  const search = useMutation({
    mutationFn: (terms?: Record<string, string>) => ok(api.POST("/works/{id}/search", { params: { path: { id: work.id }, query }, body: terms })),
    onSuccess: changed,
  });
  const grab = useMutation({
    mutationFn: () => ok(api.POST("/works/{id}/grab", { params: { path: { id: work.id }, query } })),
    onSuccess: changed,
  });
  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      await ok(api.POST("/works/{id}/candidates/{decisionId}/send", { params: { path: { id: work.id, decisionId }, query } }));
    },
    onSuccess: changed,
  });
  const blocklist = useMutation({
    mutationFn: async (decisionId: number) => {
      await ok(api.POST("/works/{id}/candidates/{decisionId}/blocklist", { params: { path: { id: work.id, decisionId }, query } }));
    },
    onSuccess: changed,
  });

  if (groups.length === 0 || group === undefined) return <p className="text-sm text-ink-muted">{t("work.notWanted", words)}</p>;
  if (!work.can_request) return <p className="text-sm text-ink-muted">Searching needs request access to this library.</p>;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        {groups.length > 1 && (
          <select
            aria-label="What to search"
            value={group}
            onChange={(event) => setGroup(event.target.value)}
            className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm"
          >
            {groups.map((entry) => (
              <option key={entry.ref} value={entry.ref}>
                {entry.label || work.title}
              </option>
            ))}
          </select>
        )}
        <Button busy={grab.isPending} busyLabel="Searching…" title="Send the best release to the download client" onClick={() => grab.mutate()}>
          {t("action.search")}
        </Button>
        <Button busy={search.isPending} busyLabel="Searching…" title="List what the indexers have, and choose" onClick={() => search.mutate(undefined)}>
          {t("action.interactive")}
        </Button>
        {grab.data && <span className="text-xs text-ink-muted">{grabNote(grab.data)}</span>}
      </div>
      {group === "" && work.wanted ? <AutomaticSearchLine wanted={work.wanted} /> : selected?.last_searched_at && (
        <p className="text-xs text-ink-muted">Last searched {relativeTime(Date.parse(selected.last_searched_at) / 1000)}.</p>
      )}
      {(info?.search_terms.length ?? 0) > 0 && (
        <DifferentTerms
          terms={info?.search_terms ?? []}
          initial={(term) => (term.label === info?.lead_people.one ? lead : term.label === info?.work.one ? work.title : "")}
          busy={search.isPending}
          onSearch={(terms) => search.mutate(terms)}
        />
      )}
      {(search.isError || grab.isError) && <p className="text-xs text-danger">{t("search.failed")}</p>}
      <IndexerProblems problems={search.data?.indexer_errors} />
      <CandidateList
        decisions={candidates.data ?? []}
        loading={candidates.isLoading}
        onSend={(decisionId) => send.mutate(decisionId)}
        sendPendingId={send.isPending ? send.variables : undefined}
        sendError={send.isError ? send.variables : undefined}
        sendErrorDetail={send.isError ? (send.error as { detail?: string }).detail : undefined}
        onBlocklist={administrator ? (decisionId) => blocklist.mutate(decisionId) : undefined}
      />
    </div>
  );
}

/** "Search with different terms": the indexers call it something else. One field per term the type takes. */
function DifferentTerms({
  terms,
  initial,
  busy,
  onSearch,
}: {
  terms: readonly { key: string; label: string }[];
  initial: (term: { key: string; label: string }) => string;
  busy: boolean;
  onSearch: (terms: Record<string, string>) => void;
}) {
  const [values, setValues] = useState<Record<string, string>>(() => Object.fromEntries(terms.map((term) => [term.key, initial(term)])));
  const ready = terms.every((term) => (values[term.key] ?? "").trim() !== "");
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-ink-muted hover:text-ink">Search with different terms</summary>
      <form
        className="mt-2 flex flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (ready) onSearch(Object.fromEntries(terms.map((term) => [term.key, (values[term.key] ?? "").trim()])));
        }}
      >
        {terms.map((term) => (
          <input
            key={term.key}
            aria-label={term.label}
            placeholder={term.label}
            value={values[term.key] ?? ""}
            onChange={(event) => setValues({ ...values, [term.key]: event.target.value })}
            className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
          />
        ))}
        <Button type="submit" disabled={busy || !ready}>
          Search these
        </Button>
      </form>
    </details>
  );
}

/** F2: when the work was and will be searched on its own, or why it will not be. */
function AutomaticSearchLine({ wanted }: { wanted: Wanted }) {
  const last = wanted.last_searched_at ? `Last searched ${relativeTime(wanted.last_searched_at)}. ` : "Not searched yet. ";
  const next = wanted.next_search_at;
  const why: Record<string, string> = {
    not_wanted: "",
    downloading: "A download for it is on its way; it is not searched meanwhile.",
    held: "A release was found and waits for room in the download client.",
    no_indexer: "Nothing is searched automatically: no indexer is enabled.",
    off: "Automatic searching is off (Settings → Priority, searches per hour).",
    no_room: "Automatic downloads for this library are paused: no free space above the reserve.",
    not_planned: "It will be scheduled for an automatic search within 5 minutes.",
    waiting_for_release: next != null ? `Searched automatically from ${new Date(next * 1000).toLocaleString()}, after its release.` : "",
    scheduled:
      next != null && next * 1000 > Date.now() ? `Next automatic search ${relativeTime(next)}.` : "Next automatic search within 15 minutes, as the hourly budget allows.",
  };
  const reason = why[wanted.automatic_search ?? "not_wanted"];
  if (!reason && wanted.automatic_search === "not_wanted") return null;
  return (
    <p className="text-xs text-ink-muted">
      {last}
      {reason}
    </p>
  );
}
