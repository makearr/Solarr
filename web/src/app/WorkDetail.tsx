import { useState } from "react";
import { useNavigate, useParams, Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Search as SearchIcon, Trash2 as TrashIcon } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { HistoryKind, historySource, historyWhen } from "@/lib/downloadHistory";
import { discCountOf, formatTrackNumber } from "@/lib/trackNumber";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { usePreference } from "@/lib/usePreference";

import { IndexerProblems } from "./IndexerProblems";
import { CandidateList } from "./CandidateList";
import { CoverArt } from "./CoverArt";
import { statusLabel, useQueue } from "./Queue";

type Edition = components["schemas"]["Edition"];
type WorkPart = components["schemas"]["WorkPart"];
type ExternalID = components["schemas"]["ExternalID"];
type Credit = components["schemas"]["Credit"];
type Wanted = components["schemas"]["Wanted"];

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
    mutationFn: async (terms?: { artist: string; album: string }) => {
      if (id === undefined) return;
      const { data, error } = await api.POST("/works/{id}/search", { params: { path: { id } }, body: terms });
      if (error) throw error;
      return data;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["work", id, "candidates"] }),
  });

  // Lidarr's automatic search: the best release goes straight to the download client.
  const grab = useMutation({
    mutationFn: async () => {
      if (id === undefined) throw new Error("no work id");
      const { data, error } = await api.POST("/works/{id}/grab", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["work", id, "candidates"] });
      void queryClient.invalidateQueries({ queryKey: ["history", "work", id] });
    },
  });

  const blocklist = useMutation({
    mutationFn: async (decisionId: number) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/candidates/{decisionId}/blocklist", {
        params: { path: { id, decisionId } },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["work", id, "candidates"] }),
  });
  const administrator = useIsAdministrator();

  const send = useMutation({
    mutationFn: async (decisionId: number) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/candidates/{decisionId}/send", {
        params: { path: { id, decisionId } },
      });
      if (error) throw error;
    },
  });

  // D16: the same remembered choice as Discover's "Start search for missing albums".
  const [searchOnMonitor] = usePreference("discover.search_on_add", true);
  const setMonitored = useMutation({
    mutationFn: async (monitored: boolean) => {
      if (id === undefined) return;
      const { error } = await api.POST("/works/{id}/monitor", {
        params: { path: { id } },
        body: { monitored, search: monitored && searchOnMonitor },
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
              <WantedBadge wanted={work.wanted} onDisk={work.on_disk} hasTracklist={work.parts.length > 0} />
              <DownloadingBadge workId={work.id} />
            </div>
            <button
              type="button"
              disabled={setMonitored.isPending || !work.can_request}
              title={work.can_request ? undefined : "Monitoring needs request access to this library"}
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
            {administrator && (
            <>
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
                      "The files go to the recycle bin rather than being deleted outright, so this " +
                      "can be undone for as long as the recycle bin keeps them.",
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
            </>
            )}
          </div>
        </header>

        {work.wanted && !work.can_request && (
          <Section title="Search">
            <p className="text-sm text-ink-muted">Searching needs request access to this library.</p>
          </Section>
        )}
        {work.wanted && work.can_request && (
          <Section title="Search">
            <div className="flex flex-col gap-2">
              <AutomaticSearchLine wanted={work.wanted} />
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  disabled={grab.isPending}
                  onClick={() => grab.mutate()}
                  title="Send the best release to the download client"
                  className="w-fit rounded-md border border-border-subtle px-3 py-1.5 text-sm
                             enabled:hover:bg-surface disabled:cursor-default disabled:opacity-50"
                >
                  {grab.isPending ? "Searching…" : "Search"}
                </button>
                <button
                  type="button"
                  disabled={search.isPending}
                  onClick={() => search.mutate(undefined)}
                  title="List what the indexers have, and choose"
                  className="w-fit rounded-md border border-border-subtle px-3 py-1.5 text-sm
                             enabled:hover:bg-surface disabled:cursor-default disabled:opacity-50"
                >
                  {search.isPending ? "Searching…" : "Interactive search"}
                </button>
                {grab.data && (
                  <span className="text-xs text-ink-muted">
                    {grab.data.already_downloading
                      ? "Already downloading."
                      : grab.data.held
                        ? "Found; waiting until the download client has room."
                        : grab.data.sent && grab.data.refused
                          ? `Sent to the download client (it refused ${grab.data.refused} better release${grab.data.refused > 1 ? "s" : ""} first).`
                          : !grab.data.sent && grab.data.refused
                            ? "The download client refused every release found -- often because it downloaded them before."
                            : grab.data.sent
                        ? "Sent to the download client."
                        : grab.data.found > 0
                          ? `${grab.data.found} found, none acceptable.`
                          : "Nothing found."}
                  </span>
                )}
              </div>
              <DifferentTerms
                artist={work.credits.find((credit) => credit.role === "artist")?.person_name ?? ""}
                album={work.title}
                busy={search.isPending}
                onSearch={(terms) => search.mutate(terms)}
              />
              {(search.isError || grab.isError) && (
                <p className="text-xs text-danger">
                  That search could not run -- check that an indexer is connected and enabled.
                </p>
              )}
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
                <EditionCard key={edition.id} edition={edition} workTitle={work.title} />
              ))}
            </ul>
          </Section>
        )}

        {work.parts.length > 0 && (
          <PartsSection
            parts={work.parts}
            discCount={discCount}
            monitorPending={setPartMonitored.isPending || !work.can_request}
            onToggleMonitored={(partId, monitored) =>
              setPartMonitored.mutate({ partId, monitored })
            }
            searchPending={searchTrack.isPending ? searchTrack.variables : undefined}
            onSearch={administrator ? (partId) => searchTrack.mutate(partId) : undefined}
            removePending={removeTrack.isPending ? removeTrack.variables.partId : undefined}
            onRemove={administrator ? (partId, deleteFiles) => removeTrack.mutate({ partId, deleteFiles }) : undefined}
          />
        )}

        <WorkHistory workId={work.id} />

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
  hasTracklist,
}: {
  wanted: Wanted | null | undefined;
  onDisk: boolean;
  hasTracklist: boolean;
}) {
  if (!wanted) return <OnDiskBadge onDisk={onDisk} />;

  // No tracks known yet: the album may well be complete on disk, so it is not called missing.
  const label = !hasTracklist && wanted.state !== "COMPLETE"
    ? "Waiting for metadata"
    : wanted.required_count != null
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

function EditionCard({ edition, workTitle }: { edition: Edition; workTitle: string }) {
  // A label that is only the provider's id is an identity key, not a name (found 2026-09-28).
  const labelIsID = edition.external_ids.some((id) => id.external_id === edition.label);
  const fields = [
    edition.format,
    edition.catalogue_number,
    edition.barcode,
    edition.year ? String(edition.year) : undefined,
  ].filter((field): field is string => Boolean(field));

  return (
    <li className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex items-center gap-2">
        <span className="font-medium">{labelIsID ? workTitle : edition.label}</span>
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
  onSearch?: (partId: number) => void;
  removePending: number | undefined;
  onRemove?: (partId: number, deleteFiles: boolean) => void;
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
            onSearch={onSearch && (() => onSearch(part.id))}
            removing={removePending === part.id}
            onRemove={onRemove && ((deleteFiles) => onRemove(part.id, deleteFiles))}
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
  /** Absent for a caller the server would refuse: searching and deleting a track are administrator-only. */
  onSearch?: () => void;
  removing: boolean;
  onRemove?: (deleteFiles: boolean) => void;
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

        {onSearch && (
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
        )}
        {onRemove && (
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
        )}
      </div>
      {confirming && onRemove && (
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
              {file ? "(it goes to the recycle bin and can be restored)" : "(there is none)"}
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

/** F2: when this album was and will be searched on its own, or why it will not be. */
function AutomaticSearchLine({ wanted }: { wanted: components["schemas"]["Wanted"] }) {
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
    waiting_for_release:
      next != null ? `Searched automatically from ${new Date(next * 1000).toLocaleString()}, after its release.` : "",
    scheduled:
      next != null && next * 1000 > Date.now()
        ? `Next automatic search ${relativeTime(next)}.`
        : "Next automatic search within 15 minutes, as the hourly budget allows.",
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

/** "3 hours ago" or "in 5 hours", from Unix seconds. */
function relativeTime(at: number): string {
  const minutes = Math.round((at * 1000 - Date.now()) / 60_000);
  const format = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });
  if (Math.abs(minutes) < 60) return format.format(minutes, "minute");
  if (Math.abs(minutes) < 48 * 60) return format.format(Math.round(minutes / 60), "hour");
  return format.format(Math.round(minutes / 1440), "day");
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

/** "Search with different terms": the indexer calls it something else. Starts from the work's own. */
function DifferentTerms({
  artist,
  album,
  busy,
  onSearch,
}: {
  artist: string;
  album: string;
  busy: boolean;
  onSearch: (terms: { artist: string; album: string }) => void;
}) {
  const [terms, setTerms] = useState({ artist, album });
  const ready = terms.artist.trim() !== "" && terms.album.trim() !== "";
  return (
    <details className="text-xs">
      <summary className="cursor-pointer text-ink-muted hover:text-ink">Search with different terms</summary>
      <form
        className="mt-2 flex flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (ready) onSearch({ artist: terms.artist.trim(), album: terms.album.trim() });
        }}
      >
        <input
          aria-label="Artist"
          value={terms.artist}
          onChange={(event) => setTerms({ ...terms, artist: event.target.value })}
          className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
        />
        <input
          aria-label="Album"
          value={terms.album}
          onChange={(event) => setTerms({ ...terms, album: event.target.value })}
          className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
        />
        <button
          type="submit"
          disabled={busy || !ready}
          className="rounded-md border border-border-subtle px-3 py-1 text-sm enabled:hover:bg-surface disabled:opacity-50"
        >
          Search these
        </button>
      </form>
    </details>
  );
}

// The album's own download history, as Lidarr's album page shows it: shown once there is any.
function WorkHistory({ workId }: { workId: number }) {
  const history = useQuery({
    queryKey: ["history", "work", workId],
    queryFn: async () => {
      const { data, error } = await api.GET("/history", { params: { query: { work_id: workId, limit: 50 } } });
      if (error) throw error;
      return data.items;
    },
  });
  if (!history.data || history.data.length === 0) return null;
  return (
    <Section title="History">
      <ul className="flex flex-col gap-1 text-sm">
        {history.data.map((entry) => (
          <li key={entry.id} className="flex items-baseline gap-3">
            <time className="shrink-0 font-mono text-xs text-ink-muted" dateTime={entry.at}>
              {historyWhen(entry)}
            </time>
            <span className="w-16 shrink-0">
              <HistoryKind kind={entry.kind} />
            </span>
            <span className="min-w-0 truncate" title={entry.release_title}>
              {entry.release_title}
              {entry.detail && (
                <span className={entry.kind === "failed" ? "text-danger" : "text-ink-muted"}> — {entry.detail}</span>
              )}
            </span>
            <span className="ml-auto shrink-0 text-xs text-ink-muted">{historySource(entry)}</span>
          </li>
        ))}
      </ul>
    </Section>
  );
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

/** "Downloading 42%" while a release for this album is in a client, as Lidarr's album page shows it. */
function DownloadingBadge({ workId }: { workId: number }) {
  const queue = useQueue(workId);
  const item = queue.data?.items[0];
  if (!item) {
    return queue.data?.held.length ? (
      <Link to="/activity/queue" className="w-fit rounded-full border border-border-subtle px-2 py-px text-xs text-ink-muted hover:underline">
        Waiting for room in the download client
      </Link>
    ) : null;
  }
  const done = item.size_bytes ? Math.round((1 - (item.size_left_bytes ?? 0) / item.size_bytes) * 100) : undefined;
  return (
    <Link
      to="/activity/queue"
      className="w-fit rounded-full border border-accent px-2 py-px text-xs text-accent hover:underline"
      title={item.release_title}
    >
      {statusLabel(item)}
      {item.state !== "IMPORTING" && done !== undefined ? ` ${done}%` : ""}
    </Link>
  );
}
