import { useEffect, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, Ban, ChevronDown, ChevronRight, HelpCircle, Undo2, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { factsOf } from "@/lib/identificationEvidence";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Dialog } from "@/ui/Dialog";

import { IdentificationCandidates } from "./IdentificationCandidates";
import { IdentifyAs, type IdentityChoice, queryFromPath } from "./IdentifyAs";
import { PathPicker, useRecentPaths } from "./PathPicker";
import { SettingRow, Switch } from "./SettingsCard";

type ImportSession = components["schemas"]["ImportSession"];
type ImportUnit = components["schemas"]["ImportUnit"];
type ImportOrigin = ImportSession["origin"];

const ORIGINS: readonly { id: ImportOrigin; label: string }[] = [
  { id: "manual", label: "Manual" },
  { id: "watched", label: "Watched folder" },
  { id: "adoption", label: "Adopt existing library" },
];

/** todo 4.21's own SSE shape (internal/importing/importjob.ImportProgress),
 *  one "topic: job" event per cluster committed. */
interface ImportProgress {
  session_id: number;
  phase: "walking" | "done" | "cancelled";
  current_unit?: string;
}

/** Live progress, keyed by session id, most recently seen phase/current unit
 *  per session. Kept outside React state until read, so a burst of events for
 *  a session nobody has selected does not re-render the whole screen. */
function useImportProgressStream(
  onSessionUpdated: (sessionId: number, progress: ImportProgress) => void,
) {
  useEffect(() => {
    const source = new EventSource("/api/v1/events?topics=job");
    source.addEventListener("update", (message) => {
      let batch: { events: { entity: string; data?: ImportProgress }[] };
      try {
        batch = JSON.parse(message.data) as typeof batch;
      } catch {
        return;
      }
      for (const event of batch.events) {
        if (!event.entity.startsWith("import_session:") || !event.data) continue;
        onSessionUpdated(event.data.session_id, event.data);
      }
    });
    return () => source.close();
    // onSessionUpdated is a fresh closure every render by design (it closes
    // over selectedSessionId); re-subscribing on every change would thrash
    // the connection, so it is intentionally excluded and read fresh via a ref.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
}

/** ADR-48: point at any path, and the pipeline (WALK/CLASSIFY/GROUP/EVIDENCE/
 *  IDENTIFY/SHAPE/DECIDE) finds and records what it contains. A COMMIT
 *  decision places a file the instant its session has a category -- given up
 *  front on the Inspect form, or assigned to a running or finished session
 *  from its own card below (todo 4.9: "decide per unit" stays available via
 *  the API, but this screen always offers assigning one). */
export function Import() {
  const [selectedSessionId, setSelectedSessionId] = useState<number | null>(null);
  const [liveProgress, setLiveProgress] = useState<Record<number, ImportProgress>>({});
  const queryClient = useQueryClient();

  useImportProgressStream((sessionId, progress) => {
    setLiveProgress((held) => ({ ...held, [sessionId]: progress }));
    void queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    if (sessionId === selectedSessionId) {
      void queryClient.invalidateQueries({ queryKey: ["import-units", sessionId] });
    }
  });

  return (
    <div className="mx-auto flex max-w-6xl flex-col gap-5 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Import</h1>
        <p className="text-sm text-ink-muted">Bring existing files into the library: a downloaded release, a watched folder, an existing library.</p>
      </header>

      <InspectForm />

      <section className="flex flex-col gap-4 rounded-lg border border-border-subtle bg-surface p-4">
        <div className="flex flex-col gap-0.5">
          <h2 className="text-sm font-medium">How imports are judged and placed</h2>
          <p className="text-xs text-ink-muted">These apply to every import, whichever folder it is.</p>
        </div>
        <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4">
          <ThresholdControl />
          <TransferLadderControl />
          <div className="flex flex-col gap-6">
            <RenameOnImportControl />
            <ExtraFilesControl />
          </div>
        </div>
      </section>

      <h2 className="text-sm font-semibold">Imports</h2>
      <div className="flex flex-col gap-4 md:flex-row md:items-start">
        <SessionList
          selectedId={selectedSessionId}
          onSelect={setSelectedSessionId}
          liveProgress={liveProgress}
        />
        {selectedSessionId !== null ? (
          <UnitList sessionId={selectedSessionId} />
        ) : (
          <p className="min-w-0 flex-1 rounded-lg border border-dashed border-border-subtle p-6 text-sm text-ink-muted">
            Pick an import on the left to see what was found in it, and what waits for your decision.
          </p>
        )}
      </div>
    </div>
  );
}

/** Lidarr's own "rename tracks" switch, at the import boundary: on, a file
 *  is named by the library's template; off, it keeps the name it arrived
 *  with. The folder structure comes from the template either way. */
function RenameOnImportControl() {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["import-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/settings");
      if (error) throw error;
      return data;
    },
  });

  const update = useMutation({
    mutationFn: async (rename: boolean) => {
      const { error } = await api.PATCH("/import/settings", {
        body: {
          auto_commit_confidence: settings.data?.auto_commit_confidence ?? 0.9,
          rename_on_import: rename,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["import-settings"] }),
  });

  if (!settings.data) return null;
  const rename = settings.data.rename_on_import ?? true;

  return (
    <fieldset className="flex flex-col gap-1 border-0 p-0 text-xs">
      <legend className="mb-1 p-0 text-sm font-medium text-ink">Naming</legend>
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={rename}
          disabled={update.isPending}
          onChange={(event) => update.mutate(event.target.checked)}
        />
        <span className="flex flex-col">
          <span className="text-ink">Rename on import</span>
          <span className="text-[0.6875rem] text-ink-muted">
            Off keeps each file's own name; the folders still follow the template.
          </span>
        </span>
      </label>
      {update.isError && <p className="text-danger">That could not be changed.</p>}
    </fieldset>
  );
}

/** Lidarr's "Import Extra Files" and its extension list: sidecars travel with the tracks. */
function ExtraFilesControl() {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["import-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/settings");
      if (error) throw error;
      return data;
    },
  });
  const [draft, setDraft] = useState<string | null>(null);

  const update = useMutation({
    mutationFn: async (body: { extra_files?: boolean; extra_file_extensions?: string[] }) => {
      const { error } = await api.PATCH("/import/settings", {
        body: { auto_commit_confidence: settings.data?.auto_commit_confidence ?? 0.9, ...body },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setDraft(null);
      void queryClient.invalidateQueries({ queryKey: ["import-settings"] });
    },
  });

  if (!settings.data) return null;
  const enabled = settings.data.extra_files ?? false;
  const saved = (settings.data.extra_file_extensions ?? []).join(",");
  const shown = draft ?? saved;

  return (
    <fieldset className="flex flex-col gap-1.5 border-0 p-0 text-xs">
      <legend className="mb-1 p-0 text-sm font-medium text-ink">Extra files</legend>
      <label className="flex items-start gap-2">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={enabled}
          disabled={update.isPending}
          onChange={(event) => update.mutate({ extra_files: event.target.checked })}
        />
        <span className="flex flex-col">
          <span className="text-ink">Import extra files</span>
          <span className="text-[0.6875rem] text-ink-muted">
            Bring matching files along with the tracks: lyrics, cue sheets, rip logs.
          </span>
        </span>
      </label>
      {enabled && (
        <label className="flex flex-col gap-1">
          <span className="text-ink">Extensions</span>
          <span className="flex items-center gap-1.5">
            <input
              value={shown}
              disabled={update.isPending}
              onChange={(event) => setDraft(event.target.value)}
              placeholder="lrc,cue,log,nfo"
              className="w-44 rounded-md border border-border-subtle bg-canvas px-1.5 py-1 font-mono text-xs text-ink
                         focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
            />
            {draft !== null && draft !== saved && (
              <button
                type="button"
                disabled={update.isPending}
                onClick={() => update.mutate({ extra_file_extensions: draft.split(",") })}
                className="rounded-md bg-accent px-2 py-0.5 text-accent-ink disabled:opacity-50"
              >
                Save
              </button>
            )}
          </span>
          <span className="text-[0.6875rem] text-ink-muted">
            Comma separated. A file named like a track takes the track&apos;s new name; others keep theirs.
          </span>
        </label>
      )}
      {update.isError && <p className="text-danger">Refused: extensions are letters and digits, such as lrc.</p>}
    </fieldset>
  );
}

/** The section 11.4 table's own five named levels for
 *  import.auto_commit_confidence -- a fixed vocabulary, not derived from
 *  anything the API returns. */
// What clears each level on the calibrated scale (documentation/music-identification.md).
const THRESHOLD_LEVELS: readonly { value: number; label: string; description: string }[] = [
  { value: 0.97, label: "Only what the files say", description: "Release and release-group IDs in the tags" },
  { value: 0.95, label: "Near-certain", description: "The above, plus an almost perfect tracklist or title match" },
  { value: 0.9, label: "Confident", description: "The above, plus close tracklist and title matches" },
  { value: 0.85, label: "Balanced", description: "Every accepted tracklist match, and fairly close titles" },
  { value: 0.8, label: "Trusting", description: "The above, plus files tagged only with a recording ID" },
];

function ThresholdControl() {
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: ["import-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/settings");
      if (error) throw error;
      return data;
    },
  });

  const update = useMutation({
    mutationFn: async (value: number) => {
      const { error } = await api.PATCH("/import/settings", {
        body: { auto_commit_confidence: value },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["import-settings"] }),
  });

  if (!settings.data) return null;
  const current = settings.data.auto_commit_confidence;

  return (
    <fieldset className="flex flex-col gap-1 border-0 p-0 text-xs">
      <legend className="mb-1 p-0 text-sm font-medium text-ink">Identification confidence</legend>
      {THRESHOLD_LEVELS.map((level) => (
        <label key={level.value} className="flex items-start gap-2">
          <input
            type="radio"
            name="threshold"
            className="mt-0.5"
            checked={current === level.value}
            disabled={update.isPending}
            onChange={() => update.mutate(level.value)}
          />
          <span className="flex flex-col">
            <span className="text-ink">
              {level.label} <span className="text-ink-muted">({level.value})</span>
            </span>
            <span className="text-[0.6875rem] text-ink-muted">{level.description}</span>
          </span>
        </label>
      ))}
      {update.isError && <p className="text-danger">That could not be changed.</p>}
    </fieldset>
  );
}

export type TransferMethod = components["schemas"]["TransferMethod"];

/** What each transfer method actually does on disk, in the terms someone
 *  choosing between them needs: whether it costs disk space, whether the
 *  source survives, and whether the library keeps depending on it. */
const TRANSFER_METHODS: readonly {
  id: TransferMethod;
  label: string;
  summary: string;
  detail: string;
}[] = [
  {
    id: "reflink",
    label: "Reflink",
    summary: "Clone, sharing disk blocks",
    detail:
      "Copy-on-write clone. Costs almost no extra disk space, because both files share the " +
      "same blocks until one is changed, and the two are fully independent afterwards. " +
      "Needs a filesystem that supports it (XFS with reflink, Btrfs, ZFS 2.2+). Be aware " +
      "that a reflink looks exactly like an ordinary copy: it has its own inode, a link " +
      "count of 1, and du reports its full size.",
  },
  {
    id: "hardlink",
    label: "Hardlink",
    summary: "A second name for the same bytes",
    detail:
      "Adds a second directory entry pointing at the same bytes, so it costs no extra disk " +
      "space at all. Both names are equal: deleting one leaves the other working. Only " +
      "possible within a single filesystem. This is the usual choice when the download " +
      "folder and the library sit on the same disk and the source must stay put, such as " +
      "a torrent that is still seeding.",
  },
  {
    id: "move",
    label: "Move",
    summary: "Relocate the file; the source is gone",
    detail:
      "Renames the file into the library, or copies it and deletes the source when a rename " +
      "is not possible across filesystems. The source no longer exists afterwards, so never " +
      "choose this for files something else still needs, such as a seeding torrent. A move " +
      "almost always succeeds, so any method listed below it is only reached if you remove " +
      "or reorder it.",
  },
  {
    id: "copy",
    label: "Copy",
    summary: "Independent bytes, full disk cost",
    detail:
      "Writes a completely independent second copy and verifies it against the source. " +
      "Uses as much extra disk space as the file itself, and works everywhere. The copy is " +
      "checked byte for byte before the import is accepted.",
  },
  {
    id: "symlink",
    label: "Symlink",
    summary: "A pointer to the source, not a real file",
    detail:
      "Leaves the library holding a pointer to the original file rather than any bytes of " +
      "its own. Costs no space, but the library entry breaks the moment the source is " +
      "moved or deleted, so it is a poor fit for anything under a download-cleanup policy.",
  },
];

/** The import transfer ladder: methods are tried top to bottom until one
 *  succeeds. Order is the whole point, so it is edited as a list rather than
 *  picked as a single value -- which method actually placed a file is
 *  invisible on disk, so the choice has to be stated here. */
export function TransferLadderControl() {
  const queryClient = useQueryClient();

  const settings = useQuery({
    queryKey: ["import-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/settings");
      if (error) throw error;
      return data;
    },
  });

  const update = useMutation({
    mutationFn: async (ladder: TransferMethod[]) => {
      const { error } = await api.PATCH("/import/settings", {
        body: {
          auto_commit_confidence: settings.data?.auto_commit_confidence ?? 0.9,
          transfer_ladder: ladder,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["import-settings"] }),
  });

  if (!settings.data) return null;
  return (
    <TransferLadderEditor
      ladder={(settings.data.transfer_ladder ?? []) as TransferMethod[]}
      busy={update.isPending}
      failed={update.isError}
      onChange={(ladder) => update.mutate(ladder)}
    />
  );
}

/** Edits one import method order; the Import screen's and each download folder's use it. */
export function TransferLadderEditor({
  ladder,
  busy,
  failed,
  onChange,
}: {
  ladder: TransferMethod[];
  busy: boolean;
  failed: boolean;
  onChange: (ladder: TransferMethod[]) => void;
}) {
  const [explaining, setExplaining] = useState(false);
  const disabled = TRANSFER_METHODS.filter((method) => !ladder.includes(method.id));

  const move = (index: number, delta: number) => {
    const target = index + delta;
    const moving = ladder[index];
    const displaced = ladder[target];
    if (moving === undefined || displaced === undefined) return;
    const next = [...ladder];
    next[index] = displaced;
    next[target] = moving;
    onChange(next);
  };

  const label = (id: TransferMethod) =>
    TRANSFER_METHODS.find((method) => method.id === id)?.label ?? id;

  return (
    <fieldset className="flex flex-col gap-1 border-0 p-0 text-xs">
      <legend className="mb-1 flex items-center gap-1 p-0 text-sm font-medium text-ink">
        Import method
        <button
          type="button"
          className="rounded p-0.5 text-ink-muted hover:text-ink"
          aria-label="What do the import methods do?"
          onClick={() => setExplaining(true)}
        >
          <HelpCircle size={12} />
        </button>
      </legend>

      {ladder.map((id, index) => (
        <div key={id} className="flex items-center gap-1">
          <input
            type="checkbox"
            checked
            disabled={busy || ladder.length === 1}
            aria-label={`Use ${label(id)}`}
            onChange={() => onChange(ladder.filter((entry) => entry !== id))}
          />
          <span className="w-4 text-ink-muted">{index + 1}.</span>
          <span className="flex-1 text-ink">{label(id)}</span>
          <button
            type="button"
            className="rounded p-0.5 text-ink-muted hover:text-ink disabled:opacity-30"
            disabled={index === 0 || busy}
            aria-label={`Move ${label(id)} up`}
            onClick={() => move(index, -1)}
          >
            <ArrowUp size={12} />
          </button>
          <button
            type="button"
            className="rounded p-0.5 text-ink-muted hover:text-ink disabled:opacity-30"
            disabled={index === ladder.length - 1 || busy}
            aria-label={`Move ${label(id)} down`}
            onClick={() => move(index, 1)}
          >
            <ArrowDown size={12} />
          </button>
        </div>
      ))}

      {disabled.length > 0 && (
        <div className="mt-1 flex flex-col gap-1 border-t border-border-subtle pt-2">
          {disabled.map((method) => (
            <div key={method.id} className="flex items-center gap-1">
              <input
                type="checkbox"
                checked={false}
                disabled={busy}
                aria-label={`Use ${method.label}`}
                onChange={() => onChange([...ladder, method.id])}
              />
              <span className="w-4" />
              <span className="flex-1 text-ink-muted">{method.label}</span>
            </div>
          ))}
        </div>
      )}

      {failed && <p className="text-danger">That could not be changed.</p>}
      {explaining && <TransferMethodHelp onClose={() => setExplaining(false)} />}
    </fieldset>
  );
}

/** The one explanation for all five methods, shown only when asked for. A
 *  plain overlay rather than a Radix dialog: no primitive is installed yet and
 *  one panel does not earn the dependency. */
function TransferMethodHelp({ onClose }: { onClose: () => void }) {
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="transfer-help-title"
        className="max-h-[80vh] w-full max-w-lg overflow-y-auto rounded-lg border border-border-subtle bg-surface p-4 text-xs shadow-lg"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="flex items-start justify-between gap-2">
          <h2 id="transfer-help-title" className="text-sm text-ink">
            Transfer methods
          </h2>
          <button
            type="button"
            className="rounded px-2 py-0.5 text-ink-muted hover:text-ink"
            onClick={onClose}
          >
            Close
          </button>
        </div>
        <p className="pt-2 text-ink-muted">
          Each method is tried in turn until one succeeds.
        </p>
        <dl className="flex flex-col gap-3 pt-3">
          {TRANSFER_METHODS.map((method) => (
            <div key={method.id}>
              <dt className="text-ink">
                {method.label}
                <span className="pl-1 text-ink-muted">{method.summary}</span>
              </dt>
              <dd className="pt-0.5 leading-relaxed text-ink-muted">{method.detail}</dd>
            </div>
          ))}
        </dl>
        <p className="pt-3 leading-relaxed text-ink-muted">
          Move and copy almost always succeed, so any method placed below them is
          normally never reached.
        </p>
      </div>
    </div>
  );
}

function InspectForm() {
  const queryClient = useQueryClient();
  // Opened from a library's own sidebar (/library/$categoryId/import): the
  // category is already known, so the form starts on it rather than making
  // the caller pick what they just navigated from.
  const { categoryId: routeCategoryId } = useParams({ strict: false }) as { categoryId?: string };
  const [sourcePath, setSourcePath] = useState("");
  const [recentPaths, rememberPath] = useRecentPaths();
  const [origin, setOrigin] = useState<ImportOrigin>("manual");
  const [categoryId, setCategoryId] = useState<number | "">(
    routeCategoryId ? Number(routeCategoryId) : "",
  );
  const [preview, setPreview] = useState(false);
  // D6: what nothing identified still becomes an album, marked unverified.
  // Off by default (asked 2026-09-30): an unrecognised folder waits for review unless asked for.
  const [addUnverified, setAddUnverified] = useState(false);
  // Instead of moving files: the folder itself becomes a root of the chosen library and is scanned in place.
  const [asRoot, setAsRoot] = useState(false);
  const categories = useCategories();

  const addRoot = useMutation({
    mutationFn: async () => {
      if (categoryId === "") return;
      const { data: root, error: rootError } = await api.POST("/rootfolders", { body: { path: sourcePath.trim() } });
      if (rootError) throw rootError;
      const { error } = await api.POST("/categories/{id}/rootfolders", {
        params: { path: { id: categoryId } },
        body: { root_folder_id: root!.id },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      rememberPath(sourcePath);
      setSourcePath("");
      void queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
    },
  });

  const inspect = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/import/inspect", {
        body: {
          source_path: sourcePath,
          origin,
          category_id: categoryId === "" ? undefined : categoryId,
          preview,
          add_unverified: addUnverified && !preview,
        },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      rememberPath(sourcePath);
      setSourcePath("");
      queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    },
  });

  return (
    <form
      className="flex flex-col gap-4 rounded-lg border border-border-subtle bg-surface p-4"
      onSubmit={(event) => {
        event.preventDefault();
        if (asRoot && categoryId !== "") addRoot.mutate();
        else inspect.mutate();
      }}
    >
      <div className="flex flex-col gap-0.5">
        <h2 className="text-sm font-medium">Import a folder</h2>
        <p className="text-xs text-ink-muted">
          Point at a downloaded release, a watched folder or a whole library. Solarr works out what is in it.
        </p>
      </div>
      <div className="flex flex-wrap items-end gap-3">
        <PathPicker
          value={sourcePath}
          onChange={setSourcePath}
          recent={recentPaths}
          placeholder="/downloads/incoming/Artist - Album"
        />
        <label className="flex flex-col gap-1 text-xs text-ink-muted">
          Origin
          <select className={inputStyle} value={origin} onChange={(event) => setOrigin(event.target.value as ImportOrigin)}>
            {ORIGINS.map((entry) => (
              <option key={entry.id} value={entry.id}>
                {entry.label}
              </option>
            ))}
          </select>
        </label>
        <label
          className="flex flex-col gap-1 text-xs text-ink-muted"
          title="Only files that fit the library's content are placed in it. Decide later keeps every unit waiting for a library."
        >
          Only into library
          <select
            className={inputStyle}
            value={categoryId}
            onChange={(event) => setCategoryId(event.target.value === "" ? "" : Number(event.target.value))}
          >
            <option value="">Decide later</option>
            {categories.data?.map((category) => (
              <option key={category.id} value={category.id}>
                {category.name}
              </option>
            ))}
          </select>
        </label>
        <button
          type="submit"
          disabled={!sourcePath.trim() || inspect.isPending || addRoot.isPending}
          className="rounded-md bg-accent px-4 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {inspect.isPending || addRoot.isPending ? "Working…" : asRoot && categoryId !== "" ? "Add root" : "Import"}
        </button>
      </div>
      <div className="flex flex-col gap-3">
        <SettingRow
          label="Preview first"
          help="Nothing is placed until you say so: every folder waits below with its candidates."
        >
          <Switch label="Preview first" checked={preview} onChange={setPreview} />
        </SettingRow>
        <SettingRow
          label="Add unrecognised folders"
          help="A folder no metadata source recognises (an album in a music library, a series or movie elsewhere) is still added, named by its own tags or folder name and marked unverified. Identify it later from the review below."
          disabled={preview}
        >
          <Switch label="Add unrecognised folders" checked={addUnverified && !preview} disabled={preview} onChange={setAddUnverified} />
        </SettingRow>
        <SettingRow
          label="Add as a root instead of moving"
          help="Nothing is moved or copied: the folder becomes a root folder of the chosen library and is scanned where it is. Needs a library."
          disabled={categoryId === ""}
        >
          <Switch
            label="Add as a root instead of moving"
            checked={asRoot && categoryId !== ""}
            disabled={categoryId === ""}
            onChange={setAsRoot}
          />
        </SettingRow>
      </div>
      {addRoot.isSuccess && (
        <p className="w-full text-xs text-ink-muted">
          Added as a root folder of the library; it is being scanned where it is. See Storage → Root folders.
        </p>
      )}
      {addRoot.isError && (
        <p className="w-full text-xs text-danger">
          {(addRoot.error as { detail?: string }).detail ?? "That folder could not be added as a root."}
        </p>
      )}
      {inspect.isError && (
        <p className="w-full text-xs text-danger">
          That could not be enqueued -- check the path and that you are an administrator.
        </p>
      )}
    </form>
  );
}

/** Shared with the per-session "assign a category" action below -- same
 *  query key as Categories.tsx/DownloadFolders.tsx, so the list is cached
 *  once across the whole app. */
function useCategories() {
  return useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });
}

const RUNNING_STATES = new Set<ImportSession["state"]>(["INSPECTING", "CANCELLING"]);
// A session that has stopped walking: the only kind that can be reverted, and
// the only kind whose held units are final enough to commit by hand. REVERTED
// is deliberately not one -- nothing may be placed into it again.
const FINISHED_STATES = new Set<ImportSession["state"]>(["DONE", "FAILED", "CANCELLED"]);

/** Rate from measured throughput, absent while the sample is small (todo
 *  4.21's own wording) -- no total is known ahead of time (units_estimated
 *  is a cheap, tag-free, possibly-undercounting pre-count, not a real
 *  count), so this reads units_committed+queued+skipped+failed against
 *  started_at rather than trusting any single "N of M" framing. */
function describeProgress(session: ImportSession): string | null {
  const processed =
    session.units_committed + session.units_queued + session.units_skipped + session.units_failed;
  const minSampleUnits = 5;
  const minSampleMs = 10_000;
  const elapsedMs = Date.now() - new Date(session.started_at).getTime();
  if (processed < minSampleUnits || elapsedMs < minSampleMs) return null;

  const perMinute = processed / (elapsedMs / 60_000);
  const rate = `~${Math.max(1, Math.round(perMinute))} units/min`;

  const estimated = session.units_estimated;
  if (estimated === undefined || estimated <= processed) return rate;
  const remainingMinutes = (estimated - processed) / perMinute;
  if (!Number.isFinite(remainingMinutes) || remainingMinutes <= 0) return rate;
  const eta =
    remainingMinutes < 1 ? "<1 min left" : `~${Math.round(remainingMinutes)} min left`;
  return `${rate} · ${eta} (of ~${estimated} est.)`;
}

function SessionList({
  selectedId,
  onSelect,
  liveProgress,
}: {
  selectedId: number | null;
  onSelect: (id: number) => void;
  liveProgress: Record<number, ImportProgress>;
}) {
  const sessions = useQuery({
    queryKey: ["import-sessions"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/sessions");
      if (error) throw error;
      return data;
    },
    refetchInterval: 5000,
  });

  return (
    <div className="flex max-h-[75vh] w-full shrink-0 flex-col gap-2 overflow-y-auto md:sticky md:top-0 md:w-96">
      {sessions.isLoading && <Message>Loading sessions…</Message>}
      {sessions.error !== null && <Message>Sessions could not be read.</Message>}
      {sessions.data && sessions.data.length === 0 && (
        <Message>No import has run yet.</Message>
      )}
      {sessions.data?.map((session) => (
        <SessionRow
          key={session.id}
          session={session}
          selected={selectedId === session.id}
          onSelect={() => onSelect(session.id)}
          progress={liveProgress[session.id]}
        />
      ))}
    </div>
  );
}

function SessionRow({
  session,
  selected,
  onSelect,
  progress,
}: {
  session: ImportSession;
  selected: boolean;
  onSelect: () => void;
  progress: ImportProgress | undefined;
}) {
  const queryClient = useQueryClient();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["import-sessions"] });

  const cancel = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/import/sessions/{id}/cancel", { params: { path: { id: session.id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const revert = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/import/sessions/{id}/revert", { params: { path: { id: session.id } } });
      if (error) throw error;
    },
    onSuccess: () => {
      invalidate();
      void queryClient.invalidateQueries({ queryKey: ["import-units", session.id] });
    },
  });

  const dismiss = useMutation({
    mutationFn: async () => {
      const { error } = await api.POST("/import/sessions/{id}/dismiss", { params: { path: { id: session.id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const commitHeld = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/import/sessions/{id}/commit", {
        params: { path: { id: session.id } },
        body: {},
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      invalidate();
      void queryClient.invalidateQueries({ queryKey: ["import-units", session.id] });
    },
  });

  return (
    <div
      className={`flex flex-col gap-1.5 rounded-lg border p-3 text-left text-xs ${
        selected ? "border-accent bg-surface" : "border-border-subtle bg-surface hover:border-accent/50"
      }`}
    >
      <div className="flex items-start gap-2">
        <button
          type="button"
          onClick={onSelect}
          className="flex min-w-0 flex-1 flex-col gap-1 text-left focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <span className="truncate font-mono text-sm text-ink" title={session.source_path}>
            {session.source_path}
          </span>
          <span className="flex flex-wrap items-center gap-1.5">
            <StateBadge state={session.state} />
            <span className="text-ink-muted">{session.origin}</span>
          </span>
          <span className="text-ink-muted">
            {session.units_committed} committed / {session.units_queued} queued /{" "}
            {session.units_skipped} skipped / {session.units_failed} failed /{" "}
            {session.units_total} total
          </span>
          {RUNNING_STATES.has(session.state) && progress?.current_unit && (
            <span className="truncate text-ink-muted" title={progress.current_unit}>
              now: {progress.current_unit}
            </span>
          )}
          {RUNNING_STATES.has(session.state) && describeProgress(session) && (
            <span className="text-ink-muted">{describeProgress(session)}</span>
          )}
          {session.error && <span className="text-danger">{session.error}</span>}
          {!selected && session.units_total > 0 && (
            <span className="text-accent">Show what was found →</span>
          )}
        </button>
        {RUNNING_STATES.has(session.state) && (
          <button
            type="button"
            onClick={() => cancel.mutate()}
            disabled={cancel.isPending}
            title="Cancel this import"
            className="shrink-0 rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <Ban aria-hidden className="size-3.5" />
          </button>
        )}
        {session.state !== "INSPECTING" && session.state !== "CANCELLING" && (
          <button
            type="button"
            onClick={() => dismiss.mutate()}
            disabled={dismiss.isPending}
            title="Dismiss this session -- it stays on record, it just leaves this list"
            className="shrink-0 rounded-md p-1 text-ink-muted hover:text-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <X aria-hidden className="size-3.5" />
          </button>
        )}
        {FINISHED_STATES.has(session.state) && session.units_committed > 0 && (
          <button
            type="button"
            onClick={() => {
              if (confirm("Revert this session? Every placed file moves back through the recycle bin.")) {
                revert.mutate();
              }
            }}
            disabled={revert.isPending}
            title="Revert this session"
            className="shrink-0 rounded-md p-1 text-ink-muted hover:text-danger disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <Undo2 aria-hidden className="size-3.5" />
          </button>
        )}
      </div>
      {session.category_id === undefined && session.state !== "REVERTED" && (
        <AssignCategory sessionId={session.id} />
      )}
      {FINISHED_STATES.has(session.state) && session.units_queued > 0 && session.category_id !== undefined && (
        <div className="flex flex-wrap items-center gap-2">
          <button
            type="button"
            onClick={() => {
              if (confirm(`Commit all ${session.units_queued} held units using their top-ranked candidate?`)) {
                commitHeld.mutate();
              }
            }}
            disabled={commitHeld.isPending}
            className="rounded-md border border-border-subtle px-2 py-1 text-ink hover:border-accent
                       disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                       focus-visible:outline-accent"
          >
            {commitHeld.isPending ? "Committing…" : `Commit all ${session.units_queued} held`}
          </button>
          {commitHeld.data && (
            <span className="text-ink-muted">
              {commitHeld.data.units_committed} committed
              {commitHeld.data.errors.length > 0 && `, ${commitHeld.data.errors.length} could not be`}
            </span>
          )}
          {commitHeld.isError && <span className="text-danger">That could not be committed.</span>}
        </div>
      )}
    </div>
  );
}

/** todo 4.9's "assign a category later": a session that started with
 *  "decide per unit" gets one from here, which also places every unit it
 *  already decided COMMIT but never placed. */
function AssignCategory({ sessionId }: { sessionId: number }) {
  const queryClient = useQueryClient();
  const categories = useCategories();
  const [categoryId, setCategoryId] = useState<number | "">("");

  const assign = useMutation({
    mutationFn: async (id: number) => {
      const { data, error } = await api.POST("/import/sessions/{id}/category", {
        params: { path: { id: sessionId } },
        body: { category_id: id },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
      void queryClient.invalidateQueries({ queryKey: ["import-units", sessionId] });
    },
  });

  return (
    <div className="flex items-center gap-1.5 border-t border-border-subtle pt-1.5">
      <select
        className={`${inputStyle} flex-1`}
        value={categoryId}
        disabled={assign.isPending}
        onChange={(event) => setCategoryId(event.target.value === "" ? "" : Number(event.target.value))}
      >
        <option value="">No category yet</option>
        {categories.data?.map((category) => (
          <option key={category.id} value={category.id}>
            {category.name}
          </option>
        ))}
      </select>
      <button
        type="button"
        disabled={categoryId === "" || assign.isPending}
        onClick={() => categoryId !== "" && assign.mutate(categoryId)}
        className="shrink-0 rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        Assign
      </button>
      {assign.isError && <span className="text-danger">Could not assign.</span>}
      {assign.isSuccess && assign.data.units_placed > 0 && (
        <span className="text-ink-muted">{assign.data.units_placed} placed</span>
      )}
    </div>
  );
}

function UnitList({ sessionId }: { sessionId: number }) {
  const units = useQuery({
    queryKey: ["import-units", sessionId],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/sessions/{id}/units", {
        params: { path: { id: sessionId }, query: { limit: 200 } },
      });
      if (error) throw error;
      return data;
    },
  });

  const queryClient = useQueryClient();
  const sessions = useQuery({
    queryKey: ["import-sessions"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/sessions");
      if (error) throw error;
      return data;
    },
  });
  const categoryId = sessions.data?.find((session) => session.id === sessionId)?.category_id ?? null;
  const importSettings = useQuery({
    queryKey: ["import-settings"],
    queryFn: async () => {
      const { data, error } = await api.GET("/import/settings");
      if (error) throw error;
      return data;
    },
  });
  // "Place all identified" (asked 2026-09-30): every waiting unit matched at or above the import's own
  // confidence; the uncertain ones stay for review.
  const threshold = importSettings.data?.auto_commit_confidence ?? 0.9;
  const identified = (units.data ?? []).filter(
    (unit) => unit.decision === "QUEUE" && (unit.candidates?.length ?? 0) > 0 && unit.confidence >= threshold,
  );
  const placeAll = useMutation({
    mutationFn: async (ids: number[]) => {
      for (const unitId of ids) {
        const { error } = await api.POST("/import/sessions/{id}/units/{unitId}/commit", {
          params: { path: { id: sessionId, unitId } },
        });
        if (error) throw error;
      }
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: ["import-units", sessionId] });
      void queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    },
  });

  return (
    <div className="min-w-0 flex-1 rounded-lg border border-border-subtle bg-surface">
      {units.data && units.data.length > 0 && (
        <div className="flex flex-wrap items-center gap-3 border-b border-border-subtle px-3 py-2 text-xs text-ink-muted">
          <span className="flex-1">{describeSessionTransfers(units.data)}</span>
          {placeAll.isError && <span className="text-danger">Placing stopped at a unit that could not be committed.</span>}
          <button
            type="button"
            disabled={identified.length === 0 || placeAll.isPending}
            onClick={() => placeAll.mutate(identified.map((unit) => unit.id))}
            title={`Places every waiting unit identified at ${Math.round(threshold * 100)}% confidence or more; the rest stay for review`}
            className="rounded-md bg-accent px-2.5 py-1 text-accent-ink disabled:opacity-50"
          >
            {placeAll.isPending ? "Placing…" : `Place all identified (${identified.length})`}
          </button>
        </div>
      )}
      {units.isLoading && <Message>Loading units…</Message>}
      {units.error !== null && <Message>Units could not be read.</Message>}
      {units.data && units.data.length === 0 && <Message>Nothing found under this path.</Message>}
      {units.data && units.data.length > 0 && (
        <ul className="divide-y divide-border-subtle">
          {units.data.map((unit) => (
            <UnitRow key={unit.id} sessionId={sessionId} unit={unit} categoryId={categoryId} />
          ))}
        </ul>
      )}
    </div>
  );
}

/** Past tense, because it already happened: "reflinked", not "reflink". The
 *  whole point of recording the method is that a reflink and a copy look
 *  identical on disk, so the word has to come from the record. */
const TRANSFER_PAST: Record<TransferMethod, string> = {
  reflink: "reflinked",
  hardlink: "hardlinked",
  move: "moved",
  copy: "copied",
  symlink: "symlinked",
};

/** How this session's files actually got into the library, counted across
 *  every unit -- a reflink and a copy are indistinguishable on disk, so the
 *  commit overview has to say which one placed them (2026-09-19 review).
 *  Units committed before the method was recorded carry none, and are
 *  reported as such rather than guessed at. */
function describeSessionTransfers(units: ImportUnit[]): string {
  const files = new Map<TransferMethod, number>();
  for (const unit of units) {
    for (const entry of unit.transfer_methods ?? []) {
      files.set(entry.method, (files.get(entry.method) ?? 0) + entry.files);
    }
  }
  if (files.size === 0) return "Nothing placed yet, so no import method to report.";
  const parts = [...files].map(([method, count]) => `${count} ${TRANSFER_PAST[method]}`);
  return `Imported by: ${parts.join(", ")}`;
}

function describeTransfer(methods: { method: TransferMethod; files: number }[]): string {
  if (methods.length === 1) return TRANSFER_PAST[methods[0]!.method];
  return methods.map((m) => `${m.files} ${TRANSFER_PAST[m.method]}`).join(", ");
}

function UnitRow({ sessionId, unit, categoryId }: { sessionId: number; unit: ImportUnit; categoryId: number | null }) {
  const [expanded, setExpanded] = useState(false);
  const [identifying, setIdentifying] = useState(false);
  const queryClient = useQueryClient();
  const library = useLibrary(categoryId === null ? undefined : String(categoryId));
  // Only a type whose held units are committed by hand offers Commit anyway and Identify as.
  const commitsHeld = useMediaTypes().find((type) => type.key === library?.media_type)?.commits_held_imports === true;

  const commit = useMutation({
    mutationFn: async (as?: IdentityChoice) => {
      const { error } = await api.POST("/import/sessions/{id}/units/{unitId}/commit", {
        params: { path: { id: sessionId, unitId: unit.id } },
        ...(as ? { body: { provider: as.provider, external_id: as.external_id } } : {}),
      });
      if (error) throw error;
    },
    onSuccess: () => {
      setIdentifying(false);
      void queryClient.invalidateQueries({ queryKey: ["import-units", sessionId] });
      void queryClient.invalidateQueries({ queryKey: ["import-sessions"] });
    },
  });

  return (
    <li className="flex flex-col gap-1.5 px-3 py-2.5 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => setExpanded((value) => !value)}
          className="flex flex-1 flex-wrap items-center gap-2 text-left focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {expanded ? (
            <ChevronDown aria-hidden className="size-3.5 shrink-0 text-ink-muted" />
          ) : (
            <ChevronRight aria-hidden className="size-3.5 shrink-0 text-ink-muted" />
          )}
          <span className="font-mono text-sm text-ink">{unit.rel_path || "(this folder)"}</span>
          {matchedAlbum(unit) && <span className="text-ink">→ {matchedAlbum(unit)}</span>}
          <ShapeBadge shape={unit.shape} />
          <DecisionBadge decision={unit.decision} />
          <span className="text-ink-muted">
            {unit.file_count} files, {Math.round(unit.confidence * 100)}% confidence
          </span>
          {(unit.transfer_methods?.length ?? 0) > 0 && (
            <span className="text-ink-muted">{describeTransfer(unit.transfer_methods!)}</span>
          )}
        </button>
        {commitsHeld && unit.decision === "QUEUE" && ((unit.candidates?.length ?? 0) > 0 || unit.work_id != null) && (
          <button
            type="button"
            onClick={() => commit.mutate()}
            disabled={commit.isPending}
            title="Place it as what it was matched to, although it was held"
            className="shrink-0 rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink
                       disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {commit.isPending ? "Committing…" : "Commit anyway"}
          </button>
        )}
        {commitsHeld && unit.decision === "QUEUE" && categoryId !== null && (
          <button
            type="button"
            onClick={() => setIdentifying(true)}
            title="Search for what these files are and place them as it"
            className="shrink-0 rounded-md border border-border-subtle px-2 py-1 text-ink-muted hover:text-ink
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Identify as…
          </button>
        )}
      </div>
      {identifying && categoryId !== null && (
        <Dialog title="Identify as…" width="wide" onClose={() => setIdentifying(false)}>
          <IdentifyAs
            categoryId={categoryId}
            guess={queryFromPath(`${unit.rel_path}/`)}
            busy={commit.isPending}
            failed={commit.isError ? ((commit.error as { detail?: string }).detail ?? "That could not be placed. Pick another or try again.") : null}
            onPlace={(choice) => commit.mutate(choice)}
          />
        </Dialog>
      )}
      {commit.isError && <span className="text-danger">{(commit.error as { detail?: string }).detail ?? "That could not be committed."}</span>}
      {expanded && (
        <div className="flex flex-col gap-1 rounded-md bg-canvas p-2 text-ink-muted">
          {unit.evidence?.signals && unit.evidence.signals.length > 0 ? (
            <ul className="flex flex-col gap-0.5">
              {unit.evidence.signals.map((signal, index) => (
                <li key={index}>
                  <span className="font-medium text-ink">{signal.kind}</span>
                  {signal.detail && `: ${signal.detail}`}
                </li>
              ))}
            </ul>
          ) : (
            <span>No evidence recorded.</span>
          )}
          {unit.candidates && unit.candidates.length > 0 && (
            <div className="border-t border-border-subtle pt-1">
              <IdentificationCandidates candidates={unit.candidates} />
            </div>
          )}
          {unit.reason && <span>Reason: {unit.reason}</span>}
        </div>
      )}
    </li>
  );
}

// What the unit was matched to, visible without expanding: a preview that hides the album is no preview (2026-09-29).
function matchedAlbum(unit: ImportUnit): string | undefined {
  const facts = factsOf(unit.candidates?.[0]?.evidence);
  return [facts.artist, facts.title].filter(Boolean).join(" — ") || undefined;
}

function StateBadge({ state }: { state: ImportSession["state"] }) {
  const isTerminal = state === "DONE" || state === "FAILED" || state === "REVERTED" || state === "CANCELLED";
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        state === "FAILED"
          ? "border-danger text-danger"
          : isTerminal
            ? "border-border-subtle text-ink-muted"
            : "border-accent text-accent"
      }`}
    >
      {state}
    </span>
  );
}

function ShapeBadge({ shape }: { shape: ImportUnit["shape"] }) {
  return (
    <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
      {shape}
    </span>
  );
}

function DecisionBadge({ decision }: { decision: ImportUnit["decision"] }) {
  return (
    <span
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        decision === "COMMIT"
          ? "border-accent text-accent"
          : decision === "FAILED"
            ? "border-danger text-danger"
            : "border-border-subtle text-ink-muted"
      }`}
    >
      {decision}
    </span>
  );
}

function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-3 text-xs text-ink-muted">{children}</p>;
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
