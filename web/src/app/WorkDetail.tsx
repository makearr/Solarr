import { useState } from "react";
import { useNavigate, useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search as SearchIcon, Trash2 as TrashIcon } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { discCountOf, formatTrackNumber } from "@/lib/trackNumber";
import { usePreference } from "@/lib/usePreference";

import { CoverArt } from "./CoverArt";

type Edition = components["schemas"]["Edition"];
type WorkPart = components["schemas"]["WorkPart"];
type ExternalID = components["schemas"]["ExternalID"];
type Credit = components["schemas"]["Credit"];
type Wanted = components["schemas"]["Wanted"];
type CandidateDecision = components["schemas"]["CandidateDecision"];

// Title, release date, credits, genres, description, editions, parts read
// straight from the work; monitoring (album and per-part) is the one write
// surface (section 19.6). No scoring/Decision yet (stage 4).
export function WorkDetail() {
  const { id: rawID } = useParams({ strict: false }) as { id?: string };
  const id = rawID !== undefined ? Number(rawID) : undefined;
  const queryClient = useQueryClient();

  const detail = useQuery({
    queryKey: ["work", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}", {
        params: { path: { id: id as number } },
      });
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => void queryClient.invalidateQueries({ queryKey: ["work", id] });

  const candidates = useQuery({
    queryKey: ["work", id, "candidates"],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}/candidates", {
        params: { path: { id: id as number } },
      });
      if (error) throw error;
      return data;
    },
  });

  const navigate = useNavigate();

  const search = useMutation({
    mutationFn: async () => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/search", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["work", id, "candidates"] }),
  });

  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/candidates/{decisionId}/send", {
        params: { path: { id, decisionId } },
      });
      if (error) throw error;
    },
  });

  const setMonitored = useMutation({
    mutationFn: async (monitored: boolean) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/monitor", {
        params: { path: { id } },
        body: { monitored },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (deleteFiles: boolean) => {
      if (id === undefined) return undefined;
      const { data, error } = await api.POST("/works/{id}/remove", {
        params: { path: { id } },
        body: { delete_files: deleteFiles },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["works"] });
      // A kept album still exists, so staying on its page is right; a deleted
      // one has no page left to show.
      if (result?.work_deleted) void navigate({ to: "/" });
      else invalidate();
    },
  });

  const setPartMonitored = useMutation({
    mutationFn: async ({ partId, monitored }: { partId: number; monitored: boolean }) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/parts/{partId}/monitor", {
        params: { path: { id, partId } },
        body: { monitored },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const removeTrack = useMutation({
    mutationFn: async ({ partId, deleteFiles }: { partId: number; deleteFiles: boolean }) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/parts/{partId}/remove", {
        params: { path: { id, partId } },
        body: { delete_files: deleteFiles },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      invalidate();
      void queryClient.invalidateQueries({ queryKey: ["works"] });
    },
  });

  const searchTrack = useMutation({
    mutationFn: async (partId: number) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/parts/{partId}/search", {
        params: { path: { id, partId } },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      invalidate();
      void queryClient.invalidateQueries({ queryKey: ["work", id, "candidates"] });
    },
  });

  if (id === undefined) return <Message>No work id in the URL.</Message>;
  if (detail.isLoading) return <Message>Loading…</Message>;
  if (detail.error !== null || !detail.data) {
    return <Message>This work could not be read.</Message>;
  }

  const work = detail.data;
  const discCount = discCountOf(work.parts);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-3xl flex-col gap-6 p-6">
        <header className="flex items-start gap-4">
          <CoverArt
            workId={work.id}
            title={work.title}
            size="full"
            className="h-40 w-40 shrink-0 rounded-lg object-cover"
          />
          <div className="flex flex-1 items-start justify-between gap-4 pt-1">
            <div className="flex flex-col gap-1.5">
              <h1 className="text-lg font-semibold">{work.title}</h1>
              <p className="text-sm text-ink-muted">
                {work.year ? `Released ${work.year}` : "Release date unknown"}
              </p>
              <WantedBadge wanted={work.wanted} onDisk={work.on_disk} />
            </div>
            <button
              type="button"
              disabled={setMonitored.isPending}
              onClick={() => setMonitored.mutate(!(work.wanted?.monitored ?? false))}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm
                         enabled:hover:bg-surface disabled:cursor-default disabled:text-ink-muted"
            >
              {work.wanted?.monitored
                ? "Monitored"
                : setMonitored.isPending
                  ? "…"
                  : "Monitor"}
            </button>
            <button
              type="button"
              disabled={remove.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    `Stop wanting ${work.title}?\n\n` +
                      "No files are deleted. If any of its files are in the library the album " +
                      "stays too, because the next scan would find them again.",
                  )
                ) {
                  remove.mutate(false);
                }
              }}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                         enabled:hover:text-danger disabled:cursor-default"
            >
              Remove
            </button>
            <button
              type="button"
              disabled={remove.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    `Delete ${work.title} and its files?\n\n` +
                      "The files go to quarantine rather than being deleted outright, so this " +
                      "can be undone for as long as quarantine keeps them.",
                  )
                ) {
                  remove.mutate(true);
                }
              }}
              className="shrink-0 rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                         enabled:hover:text-danger disabled:cursor-default"
            >
              Delete files
            </button>
          </div>
        </header>

        {work.wanted && (
          <Section title="Search">
            <div className="flex flex-col gap-2">
              <button
                type="button"
                disabled={search.isPending}
                onClick={() => search.mutate()}
                className="w-fit rounded-md border border-border-subtle px-3 py-1.5 text-sm
                           enabled:hover:bg-surface disabled:cursor-default disabled:opacity-50"
              >
                {search.isPending ? "Searching…" : "Search now"}
              </button>
              {search.isError && (
                <p className="text-xs text-danger">
                  That search could not run -- check that an indexer is connected and enabled.
                </p>
              )}
              <CandidateList
                decisions={candidates.data ?? []}
                loading={candidates.isLoading}
                onSend={(decisionId) => send.mutate(decisionId)}
                sendPendingId={send.isPending ? send.variables : undefined}
                sendError={send.isError ? send.variables : undefined}
              />
            </div>
          </Section>
        )}

        {work.description && (
          <Section title="Description">
            <p className="text-sm leading-relaxed">{work.description}</p>
          </Section>
        )}

        {work.credits.length > 0 && (
          <Section title="Credits">
            <CreditList credits={work.credits} />
          </Section>
        )}

        {work.genres.length > 0 && (
          <Section title="Genres">
            <ul className="flex flex-wrap gap-1.5">
              {work.genres.map((genre) => (
                <li
                  key={genre}
                  className="rounded-full border border-border-subtle px-2 py-0.5 text-xs text-ink-muted"
                >
                  {genre}
                </li>
              ))}
            </ul>
          </Section>
        )}

        {work.editions.length > 0 && (
          <Section title={`Editions (${work.editions.length})`}>
            <ul className="flex flex-col gap-2">
              {work.editions.map((edition) => (
                <EditionCard key={edition.id} edition={edition} />
              ))}
            </ul>
          </Section>
        )}

        {work.parts.length > 0 && (
          <PartsSection
            parts={work.parts}
            discCount={discCount}
            monitorPending={setPartMonitored.isPending}
            onToggleMonitored={(partId, monitored) =>
              setPartMonitored.mutate({ partId, monitored })
            }
            searchPending={searchTrack.isPending ? searchTrack.variables : undefined}
            onSearch={(partId) => searchTrack.mutate(partId)}
            removePending={removeTrack.isPending ? removeTrack.variables.partId : undefined}
            onRemove={(partId, deleteFiles) => removeTrack.mutate({ partId, deleteFiles })}
          />
        )}

        {work.external_ids.length > 0 && (
          <Section title="External IDs">
            <ExternalIDList ids={work.external_ids} />
          </Section>
        )}
      </div>
    </div>
  );
}

// WantedBadge renders the real per-track fact (section 19.6's "9 of 12
// tracks") once something has ever wanted this album, falling back to
// on_disk's coarser badge otherwise.
function WantedBadge({
  wanted,
  onDisk,
}: {
  wanted: Wanted | null | undefined;
  onDisk: boolean;
}) {
  if (!wanted) return <OnDiskBadge onDisk={onDisk} />;

  const label =
    wanted.required_count != null
      ? `${wanted.present_count} of ${wanted.required_count} tracks`
      : wanted.state === "COMPLETE"
        ? "Complete"
        : wanted.present_count > 0
          ? `${wanted.present_count} tracks`
          : "Missing";

  return (
    <span
      className={`w-fit rounded-full border px-2 py-0.5 text-xs ${
        wanted.state === "COMPLETE"
          ? "border-current text-accent"
          : "border-border-subtle text-ink-muted"
      }`}
    >
      {label}
    </span>
  );
}

function OnDiskBadge({ onDisk }: { onDisk: boolean }) {
  return (
    <span
      className={`w-fit rounded-full border px-2 py-0.5 text-xs ${
        onDisk
          ? "border-current text-accent"
          : "border-border-subtle text-ink-muted"
      }`}
    >
      {onDisk ? "On disk" : "Missing"}
    </span>
  );
}

// Grouped by role (artist, producer, songwriter, ...) so a long credit list
// reads as who-did-what rather than a flat name dump.
function CreditList({ credits }: { credits: readonly Credit[] }) {
  const byRole = new Map<string, Credit[]>();
  for (const credit of credits) {
    const group = byRole.get(credit.role) ?? [];
    group.push(credit);
    byRole.set(credit.role, group);
  }

  return (
    <dl className="flex flex-col gap-2">
      {[...byRole.entries()].map(([role, people]) => (
        <div key={role} className="flex items-baseline gap-2 text-sm">
          <dt className="w-24 shrink-0 capitalize text-ink-muted">{role}</dt>
          <dd className="flex flex-wrap gap-x-2 gap-y-1">
            {people.map((credit) => (
              <Link
                key={credit.person_id}
                to="/people/$id"
                params={{ id: String(credit.person_id) }}
                className="hover:underline"
              >
                {credit.person_name}
              </Link>
            ))}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function EditionCard({ edition }: { edition: Edition }) {
  const fields = [
    edition.format,
    edition.catalogue_number,
    edition.barcode,
    edition.year ? String(edition.year) : undefined,
  ].filter((field): field is string => Boolean(field));

  return (
    <li className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex items-center gap-2">
        <span className="font-medium">{edition.label}</span>
        <span className="text-xs text-ink-muted">{edition.kind}</span>
        {edition.is_default && (
          <span className="rounded-full border border-current px-1.5 py-px text-xs text-accent">
            default
          </span>
        )}
      </div>
      {fields.length > 0 && <p className="text-xs text-ink-muted">{fields.join(" · ")}</p>}
      {edition.external_ids.length > 0 && <ExternalIDList ids={edition.external_ids} />}
    </li>
  );
}

// The columns a track row can carry beyond number, title and duration
// (2026-09-19). Each is off until someone turns it on, and the choice is
// stored per user rather than per browser.
const OPTIONAL_COLUMNS = [
  { key: "released", label: "Release date" },
  { key: "searched", label: "Last searched" },
  { key: "codec", label: "Codec" },
  { key: "bitrate", label: "Bitrate" },
] as const;

type ColumnKey = (typeof OPTIONAL_COLUMNS)[number]["key"];

function PartsSection({
  parts,
  discCount,
  monitorPending,
  onToggleMonitored,
  searchPending,
  onSearch,
  removePending,
  onRemove,
}: {
  parts: readonly WorkPart[];
  discCount: number;
  monitorPending: boolean;
  onToggleMonitored: (partId: number, monitored: boolean) => void;
  searchPending: number | undefined;
  onSearch: (partId: number) => void;
  removePending: number | undefined;
  onRemove: (partId: number, deleteFiles: boolean) => void;
}) {
  const [columns, setColumns] = usePreference<ColumnKey[]>("album.columns", []);
  const present = parts.filter((part) => part.file != null).length;

  const toggle = (key: ColumnKey) =>
    setColumns(columns.includes(key) ? columns.filter((c) => c !== key) : [...columns, key]);

  return (
    <section className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">
          Tracks ({present} of {parts.length})
        </h2>
        <div className="ml-auto flex flex-wrap gap-x-3 gap-y-1 text-[0.6875rem] text-ink-muted">
          {OPTIONAL_COLUMNS.map(({ key, label }) => (
            <label key={key} className="flex items-center gap-1">
              <input type="checkbox" checked={columns.includes(key)} onChange={() => toggle(key)} />
              {label}
            </label>
          ))}
        </div>
      </div>
      <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {parts.map((part) => (
          <PartRow
            key={part.id}
            part={part}
            discCount={discCount}
            columns={columns}
            pending={monitorPending}
            onToggleMonitored={(monitored) => onToggleMonitored(part.id, monitored)}
            searching={searchPending === part.id}
            onSearch={() => onSearch(part.id)}
            removing={removePending === part.id}
            onRemove={(deleteFiles) => onRemove(part.id, deleteFiles)}
          />
        ))}
      </ul>
    </section>
  );
}

function PartRow({
  part,
  discCount,
  columns,
  pending,
  onToggleMonitored,
  searching,
  onSearch,
  removing,
  onRemove,
}: {
  part: WorkPart;
  discCount: number;
  columns: readonly ColumnKey[];
  pending: boolean;
  onToggleMonitored: (monitored: boolean) => void;
  searching: boolean;
  onSearch: () => void;
  removing: boolean;
  onRemove: (deleteFiles: boolean) => void;
}) {
  const file = part.file;
  const title = part.title ?? part.media_ref;
  const [confirming, setConfirming] = useState(false);
  const [deleteFiles, setDeleteFiles] = useState(false);
  return (
    <li className="flex flex-col">
      <div className="flex items-center gap-3 px-3 py-2 text-sm">
        <button
          type="button"
          role="switch"
          aria-checked={part.monitored}
          aria-label={part.monitored ? "Unmonitor this track" : "Monitor this track"}
          disabled={pending}
          onClick={() => onToggleMonitored(!part.monitored)}
          className={`h-3.5 w-3.5 shrink-0 rounded-sm border disabled:cursor-default ${
            part.monitored
              ? "border-accent bg-accent"
              : "border-border-subtle bg-transparent"
          }`}
        />
        <span
          aria-label={file ? "In the library" : "Missing"}
          title={file ? "In the library" : "Missing"}
          className={`size-2 shrink-0 rounded-full ${file ? "bg-ok" : "bg-danger"}`}
        />
        <span className="w-10 shrink-0 text-right font-mono text-xs tabular-nums text-ink-muted">
          {formatTrackNumber(part.media_ref, part.sort_key, discCount)}
        </span>
        <span className="truncate">{title}</span>
        {part.runtime_ms != null && (
          <span className="shrink-0 font-mono text-xs text-ink-muted">
            {formatDuration(part.runtime_ms)}
          </span>
        )}

        <span className="ml-auto flex shrink-0 items-center gap-3 font-mono text-[0.6875rem] text-ink-muted">
          {columns.includes("released") && <span className="w-20 text-right">{part.date ?? "--"}</span>}
          {columns.includes("searched") && (
            <span className="w-20 text-right">{formatLastSearched(part.wanted?.last_searched_at)}</span>
          )}
          {columns.includes("codec") && (
            <span className="w-16 text-right">{file?.audio_codec ?? file?.video_codec ?? "--"}</span>
          )}
          {columns.includes("bitrate") && (
            <span className="w-20 text-right">
              {file && file.bitrate_kbps > 0 ? `${file.bitrate_kbps} kbps` : "--"}
            </span>
          )}
          {file?.meets_target != null && (
            <QualityMark label="target" met={file.meets_target} what="the library's target quality" />
          )}
          {file?.meets_cutoff != null && (
            <QualityMark label="cutoff" met={file.meets_cutoff} what="the cutoff, so it will not be upgraded" />
          )}
        </span>

        <button
          type="button"
          aria-label={`Search for ${part.title ?? part.media_ref}`}
          title="Want this track on its own and search every indexer for it"
          disabled={searching}
          onClick={onSearch}
          className="flex shrink-0 items-center gap-1 rounded-full border border-border-subtle px-1.5 py-0.5
                     text-[0.6875rem] text-ink-muted hover:text-ink disabled:opacity-50"
        >
          <SearchIcon aria-hidden className="size-3" />
          {searching ? "Searching…" : "Search"}
        </button>
        <button
          type="button"
          aria-label={`Delete ${title}`}
          title="Delete this track"
          aria-expanded={confirming}
          onClick={() => setConfirming(!confirming)}
          className="shrink-0 rounded p-0.5 text-ink-muted hover:text-danger"
        >
          <TrashIcon aria-hidden className="size-3.5" />
        </button>
      </div>
      {confirming && (
        <div className="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border-subtle bg-canvas px-3 py-2 text-xs">
          <span>Stop wanting {title}?</span>
          <label className="flex items-center gap-1">
            <input
              type="checkbox"
              checked={deleteFiles}
              disabled={!file}
              onChange={(event) => setDeleteFiles(event.target.checked)}
            />
            Also delete the file on disk
            <span className="text-ink-muted">
              {file ? "(it goes to quarantine and can be restored)" : "(there is none)"}
            </span>
          </label>
          <span className="ml-auto flex gap-2">
            <button
              type="button"
              onClick={() => setConfirming(false)}
              className="rounded-md border border-border-subtle px-2 py-0.5 text-ink-muted hover:text-ink"
            >
              Cancel
            </button>
            <button
              type="button"
              disabled={removing}
              onClick={() => {
                onRemove(deleteFiles && file != null);
                setConfirming(false);
              }}
              className="rounded-md border border-danger px-2 py-0.5 text-danger disabled:opacity-50"
            >
              {removing ? "Deleting…" : "Delete"}
            </button>
          </span>
        </div>
      )}
    </li>
  );
}

// Whether a file meets its library's target or cutoff (2026-09-19 review).
function QualityMark({ label, met, what }: { label: string; met: boolean; what: string }) {
  return (
    <span
      title={met ? `Meets ${what}` : `Does not meet ${what}`}
      className={`rounded-full px-1.5 py-px font-sans ${
        met ? "bg-ok/15 text-ok" : "border border-border-subtle text-ink-muted line-through"
      }`}
    >
      {label}
    </span>
  );
}

function formatLastSearched(at: number | undefined): string {
  if (at == null || at === 0) return "never";
  return new Date(at * 1000).toLocaleDateString();
}

function ExternalIDList({ ids }: { ids: readonly ExternalID[] }) {
  return (
    <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-ink-muted">
      {ids.map((id) => (
        <li key={`${id.provider}:${id.external_id}`} className="font-mono">
          {id.provider}: {id.external_id}
        </li>
      ))}
    </ul>
  );
}

function formatDuration(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

// CandidateList is F16's first slice: every release ever decided against
// this work's own wanted album, newest first, with its score breakdown --
// no dispatch action yet (stage 5's own next pass).
function CandidateList({
  decisions,
  loading,
  onSend,
  sendPendingId,
  sendError,
}: {
  decisions: CandidateDecision[];
  loading: boolean;
  onSend: (decisionId: number) => void;
  sendPendingId: number | undefined;
  sendError: number | undefined;
}) {
  if (loading) return <p className="text-xs text-ink-muted">Loading…</p>;
  if (decisions.length === 0) {
    return <p className="text-xs text-ink-muted">Nothing found yet -- try Search now.</p>;
  }
  return (
    <ul className="flex flex-col gap-2">
      {decisions.map((decision) => (
        <li
          key={decision.id}
          className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3 text-sm"
        >
          <div className="flex flex-wrap items-center gap-2">
            <OutcomeBadge outcome={decision.outcome} />
            <span className="truncate font-mono text-xs" title={decision.title}>
              {decision.title}
            </span>
            <span className="ml-auto shrink-0 text-xs text-ink-muted">
              score {decision.score.toFixed(1)}
            </span>
            {decision.outcome === "ACCEPT" && (
              <button
                type="button"
                onClick={() => onSend(decision.id)}
                disabled={sendPendingId === decision.id}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-0.5 text-xs
                           text-ink-muted hover:text-ink disabled:opacity-50"
              >
                {sendPendingId === decision.id ? "Sending…" : "Send"}
              </button>
            )}
          </div>
          {sendError === decision.id && (
            <p className="text-xs text-danger">
              That could not be sent -- check that a matching download client is connected.
            </p>
          )}
          <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-ink-muted">
            <span>{decision.protocol}</span>
            {decision.size_bytes != null && <span>{formatBytes(decision.size_bytes)}</span>}
            {decision.seeders != null && <span>{decision.seeders} seeders</span>}
            {decision.match_confidence != null && (
              <span>{Math.round(decision.match_confidence * 100)}% match</span>
            )}
            <span>{new Date(decision.decided_at).toLocaleString()}</span>
          </div>
          {decision.rejections.length > 0 && (
            <ul className="text-xs text-danger">
              {decision.rejections.map((rejection, index) => (
                <li key={index}>
                  {rejection.field}: wanted {rejection.wanted}, got {rejection.got}
                </li>
              ))}
            </ul>
          )}
          {decision.deferrals.length > 0 && (
            <ul className="text-xs text-warn">
              {decision.deferrals.map((deferral, index) => (
                <li key={index}>{deferral.reason}</li>
              ))}
            </ul>
          )}
        </li>
      ))}
    </ul>
  );
}

function OutcomeBadge({ outcome }: { outcome: CandidateDecision["outcome"] }) {
  const tone =
    outcome === "ACCEPT" ? "text-ok border-border-subtle" : outcome === "DEFER" ? "text-warn border-warn" : "text-danger border-danger";
  return (
    <span className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${tone}`}>{outcome}</span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let unitIndex = 0;
  while (value >= 1024 && unitIndex < units.length - 1) {
    value /= 1024;
    unitIndex += 1;
  }
  return `${value.toFixed(1)} ${units[unitIndex]}`;
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{title}</h2>
      {children}
    </section>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}
