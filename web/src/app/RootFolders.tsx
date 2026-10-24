import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { HardDrive, Pencil, Plus, X } from "lucide-react";

import { api, ok } from "@/api/client";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import { useLibraryLabel } from "@/lib/libraryLabel";
import { useCategories, useRootFolders } from "@/lib/resources";
import type { components } from "@/api/schema";
import { EditCategoryForm } from "./Categories";
import { Button } from "@/ui/Button";
import { ConfirmDialog } from "@/ui/ConfirmDialog";
import { inputStyle } from "@/ui/form";
import { NewLibraryFields, useNewLibrary } from "./NewLibrary";
import { RootMoveControls } from "./Moves";

type RootFolder = components["schemas"]["RootFolder"];
type LibraryCategory = components["schemas"]["LibraryCategory"];

const NEW_LIBRARY = "__new__";

/** Every library root with its placement policy and what the last capacity sweep found (todo 4.18): "capacity is
 *  polled, not assumed" (section 11.7). Part of Storage -> Folders (2026-10-23). */
export function RootFolderSection() {
  const queryClient = useQueryClient();
  const roots = useRootFolders();
  const categories = useCategories();
  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["rootfolders"] });

  const attachCategory = useMutation({
    mutationFn: async (args: { rootFolderId: number; categoryId: number }) => {
      await ok(api.POST("/categories/{id}/rootfolders", {
        params: { path: { id: args.categoryId } },
        body: { root_folder_id: args.rootFolderId },
      }));
    },
    onSuccess: invalidate,
  });

  const detachCategory = useMutation({
    mutationFn: async (args: { rootFolderId: number; categoryId: number }) => {
      await ok(api.DELETE("/categories/{id}/rootfolders/{rootFolderId}", {
        params: { path: { id: args.categoryId, rootFolderId: args.rootFolderId } },
      }));
    },
    onSuccess: invalidate,
  });

  const setPolicy = useMutation({
    mutationFn: async (args: { id: number; readOnly: boolean; allowRename: boolean; minFreeBytes: number }) => {
      await ok(api.PATCH("/rootfolders/{id}", {
        params: { path: { id: args.id } },
        body: { read_only: args.readOnly, allow_rename: args.allowRename, min_free_bytes: args.minFreeBytes },
      }));
    },
    onSuccess: invalidate,
  });

  const setOnline = useMutation({
    mutationFn: async (args: { id: number; online: boolean }) => {
      await ok(api.PATCH("/rootfolders/{id}/online", {
        params: { path: { id: args.id } },
        body: { online: args.online },
      }));
    },
    onSuccess: invalidate,
  });

  // Refused (409) while any library still holds this root: take it out of every library first (settings.ErrInUse).
  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/rootfolders/{id}", { params: { path: { id } } }));
    },
    onSuccess: invalidate,
  });

  return (
    <section className="flex flex-col gap-3">
      <header className="flex flex-col gap-0.5">
        <h2 className="text-sm font-semibold">Root folders</h2>
        <p className="text-xs leading-relaxed text-ink-muted">
          Where each library keeps its files. One that cannot be reached is declared offline by Solarr and back online
          once it is reached again, checked every five minutes; one you declare offline stays offline until you declare
          it online.
        </p>
      </header>

      {roots.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {roots.error !== null && <p className="text-sm text-danger">Root folders could not be read.</p>}

      {roots.data && roots.data.length > 0 ? (
        <div className="flex flex-col gap-4">
          {groupByLibrary(roots.data, categories.data ?? []).map((group) => (
            <section key={group.key} className="flex flex-col gap-1.5">
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                <h3 className="text-xs font-medium">{group.name}</h3>
                <span className="text-xs text-ink-muted">{libraryCapacity(group.roots)}</span>
              </div>
              <ul className="flex flex-col gap-2">
                {group.roots.map((root) => (
                  <RootFolderCard
                    key={root.id}
                    root={root}
                    categories={categories.data ?? []}
                    onSavePolicy={(readOnly, allowRename, minFreeBytes) =>
                      setPolicy.mutate({ id: root.id, readOnly, allowRename, minFreeBytes })
                    }
                    onToggleOnline={() => setOnline.mutate({ id: root.id, online: !root.online })}
                    onAttachCategory={(categoryId) => attachCategory.mutate({ rootFolderId: root.id, categoryId })}
                    onDetachCategory={(categoryId) => detachCategory.mutate({ rootFolderId: root.id, categoryId })}
                    onLibraryEdited={() => {
                      invalidate();
                      void queryClient.invalidateQueries({ queryKey: ["categories"] });
                    }}
                    onRemove={() => remove.mutate(root.id)}
                    busy={setPolicy.isPending || setOnline.isPending}
                    categoryBusy={attachCategory.isPending || detachCategory.isPending}
                    removeBusy={remove.isPending && remove.variables === root.id}
                    removeError={remove.isError && remove.variables === root.id ? remove.error : null}
                  />
                ))}
              </ul>
            </section>
          ))}
        </div>
      ) : (
        !roots.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No library root is configured.
          </p>
        )
      )}

      {(setPolicy.isError || setOnline.isError || detachCategory.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddRootFolder
        categories={categories.data ?? []}
        onAdded={() => {
          invalidate();
          void queryClient.invalidateQueries({ queryKey: ["categories"] });
        }}
      />
    </section>
  );
}

interface LibraryGroup {
  key: string;
  name: string;
  roots: RootFolder[];
}

// A root folder sits under the one library it belongs to; one in none gets a group of its own.
function groupByLibrary(roots: RootFolder[], categories: LibraryCategory[]): LibraryGroup[] {
  const groups: LibraryGroup[] = categories
    .map((category) => ({
      key: `library-${category.id}`,
      name: category.name,
      roots: roots.filter((root) => (root.categories ?? []).some((attached) => attached.id === category.id)),
    }))
    .filter((group) => group.roots.length > 0);
  const unassigned = roots.filter((root) => (root.categories ?? []).length === 0);
  if (unassigned.length > 0) groups.push({ key: "none", name: "Not in a library", roots: unassigned });
  return groups;
}

/** Section 11.7/10.4: an offline root's stale bytes are not free space, and roots on one filesystem count once. */
function libraryCapacity(roots: RootFolder[]): string {
  const online = oneRootPerFilesystem(roots.filter((root) => root.online && (root.total_bytes ?? 0) > 0));
  const offline = roots.filter((root) => !root.online).length;
  const total = online.reduce((sum, root) => sum + (root.total_bytes ?? 0), 0);
  const free = online.reduce((sum, root) => sum + (root.free_bytes ?? 0), 0);
  const measured = total > 0 ? `${formatBytes(free)} free of ${formatBytes(total)}` : "not measured yet";
  return offline > 0 ? `${measured} · ${offline} offline, excluded` : measured;
}

// Two roots on one filesystem share its space; count it once. Separate ZFS datasets of one pool
// have their own ids and may still overlap -- the note under the totals says so.
function oneRootPerFilesystem(roots: RootFolder[]): RootFolder[] {
  const seen = new Set<string>();
  return roots.filter((root) => {
    if (!root.filesystem_id) return true;
    if (seen.has(root.filesystem_id)) return false;
    seen.add(root.filesystem_id);
    return true;
  });
}

function RootFolderCard({
  root,
  categories,
  onSavePolicy,
  onToggleOnline,
  onAttachCategory,
  onDetachCategory,
  onLibraryEdited,
  onRemove,
  busy,
  categoryBusy,
  removeBusy,
  removeError,
}: {
  root: RootFolder;
  categories: LibraryCategory[];
  onSavePolicy: (readOnly: boolean, allowRename: boolean, minFreeBytes: number) => void;
  onToggleOnline: () => void;
  onAttachCategory: (categoryId: number) => void;
  onDetachCategory: (categoryId: number) => void;
  onLibraryEdited: () => void;
  onRemove: () => void;
  busy: boolean;
  categoryBusy: boolean;
  removeBusy: boolean;
  removeError: unknown;
}) {
  const libraryLabel = useLibraryLabel();
  const [readOnly, setReadOnly] = useState(root.read_only ?? false);
  const [allowRename, setAllowRename] = useState(root.allow_rename ?? false);
  const [minFreeBytes, setMinFreeBytes] = useState(root.min_free_bytes ?? 0);
  const [editingLibraryId, setEditingLibraryId] = useState<number | undefined>();
  const [confirming, setConfirming] = useState<"remove" | { detach: { id: number; name: string } }>();
  const attached = root.categories ?? [];
  const dirty =
    readOnly !== (root.read_only ?? false) ||
    allowRename !== (root.allow_rename ?? false) ||
    minFreeBytes !== (root.min_free_bytes ?? 0);

  const total = root.total_bytes ?? 0;
  const free = root.free_bytes ?? 0;
  const used = total > 0 ? total - free : 0;
  const usedPct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const reservePct = total > 0 ? Math.min(100, Math.round(((root.min_free_bytes ?? 0) / total) * 100)) : 0;
  const name = root.label || root.path;

  return (
    <li className="flex flex-col gap-1.5 rounded-lg border border-border-subtle bg-surface px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <HardDrive aria-hidden className="size-4 shrink-0 text-ink-muted" />
        <span className="text-sm font-medium">{name}</span>
        {root.label && <span className="truncate font-mono text-ink-muted">{root.path}</span>}
        <Badge tone={root.online ? "ok" : "warn"} title={root.offline_reason}>
          {root.online ? "online" : root.offline_auto ? "offline: unreachable" : "offline"}
        </Badge>
        {root.writable !== undefined && !root.writable && (
          <Badge tone="warn">{root.read_only_detected ? "read-only filesystem" : "not writable"}</Badge>
        )}
        {root.drain_state && root.drain_state !== "NONE" && <Badge tone="warn">{root.drain_state.toLowerCase()}</Badge>}
        <span
          className="ml-auto flex items-center gap-2 text-ink-muted"
          title={
            root.capacity_checked_at
              ? `${formatBytes(free)} free · measured ${new Date(root.capacity_checked_at).toLocaleString()}`
              : undefined
          }
        >
          {total > 0 ? (
            <>
              <span className="relative h-1.5 w-28 overflow-hidden rounded-full bg-canvas">
                <span className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${usedPct}%` }} />
                {reservePct > 0 && (
                  <span className="absolute inset-y-0 border-l-2 border-dashed border-danger/70" style={{ left: `${100 - reservePct}%` }} />
                )}
              </span>
              {formatBytes(free)} free of {formatBytes(total)}
            </>
          ) : (
            "not measured yet"
          )}
        </span>
      </div>
      {!root.online && root.offline_auto && root.offline_reason && (
        <p className="text-warn">Solarr cannot reach it: {root.offline_reason}. It comes back online by itself once reached.</p>
      )}

      {(root.vanished_held ?? 0) > 0 && <VanishedHeld root={root} />}

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <span className="flex flex-wrap items-center gap-1">
          {attached.map((category) => (
            <span
              key={category.id}
              className={`flex items-center gap-1 rounded-full border py-px pl-2 pr-1 ${
                editingLibraryId === category.id ? "border-accent" : "border-border-subtle"
              }`}
            >
              <button
                type="button"
                title={`Edit the ${category.name} library`}
                aria-expanded={editingLibraryId === category.id}
                onClick={() => setEditingLibraryId(editingLibraryId === category.id ? undefined : category.id)}
                className="flex items-center gap-1 hover:text-accent"
              >
                {libraryLabel(category)}
                <Pencil aria-hidden className="size-2.5 text-ink-muted" />
              </button>
              <button
                type="button"
                aria-label={`Take this root folder out of ${category.name}`}
                onClick={() => setConfirming({ detach: category })}
                disabled={categoryBusy}
                className="rounded-full p-0.5 text-ink-muted hover:text-danger disabled:opacity-50"
              >
                <X aria-hidden className="size-2.5" />
              </button>
            </span>
          ))}
          {attached.length === 0 && (
            <AttachCategory alreadyAttached={attached} categories={categories} busy={categoryBusy} onAttach={onAttachCategory} />
          )}
        </span>
        <label className="flex items-center gap-1 text-ink-muted">
          <input
            type="checkbox"
            checked={readOnly || root.read_only_detected === true}
            disabled={busy || root.read_only_detected === true}
            title={root.read_only_detected ? "The filesystem is mounted read only, so this is on whatever you choose here." : undefined}
            onChange={(event) => setReadOnly(event.target.checked)}
            className="size-3.5"
          />
          read only{root.read_only_detected ? " (detected)" : ""}
        </label>
        <label className="flex items-center gap-1 text-ink-muted">
          <input type="checkbox" checked={allowRename} disabled={busy} onChange={(event) => setAllowRename(event.target.checked)} className="size-3.5" />
          allow rename
        </label>
        <label className="flex items-center gap-1 text-ink-muted">
          reserve
          <input
            type="number"
            min={0}
            value={Math.round(minFreeBytes / 1_000_000_000)}
            disabled={busy}
            onChange={(event) => setMinFreeBytes(Number(event.target.value) * 1_000_000_000)}
            className="w-14 rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs text-ink
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
          GB
        </label>
        {dirty && (
          <Button variant="primary" className="!px-2 !py-0.5 !text-xs" disabled={busy} onClick={() => onSavePolicy(readOnly, allowRename, minFreeBytes)}>
            Save
          </Button>
        )}
      </div>
      <RootMoveControls root={root}>
        <Button variant="ghost" disabled={busy} onClick={onToggleOnline}>
          {root.online ? "Declare offline" : "Declare online"}
        </Button>
        <Button
          variant="ghost"
          className="enabled:hover:!text-danger"
          disabled={removeBusy}
          title={attached.length > 0 ? "Take it out of its library first" : "Remove this root folder"}
          onClick={() => setConfirming("remove")}
        >
          Remove
        </Button>
      </RootMoveControls>
      {attached.length === 0 && (
        <p className="text-warn">Not in any library -- nothing will place files here until it is added to one.</p>
      )}
      {editingLibraryId !== undefined && attached.some((category) => category.id === editingLibraryId) && (
        <div className="rounded-md border border-border-subtle bg-canvas p-3">
          <EditCategoryForm
            key={editingLibraryId}
            categoryId={editingLibraryId}
            onDone={() => {
              setEditingLibraryId(undefined);
              onLibraryEdited();
            }}
            onCancel={() => setEditingLibraryId(undefined)}
          />
        </div>
      )}
      {removeError !== null && (
        <p className="text-danger">{(removeError as { detail?: string }).detail ?? "That root folder could not be removed."}</p>
      )}

      {confirming === "remove" && (
        <ConfirmDialog
          title={`Remove ${name}?`}
          consequence="Solarr forgets this root folder. Nothing on disk is touched."
          confirmLabel="Remove"
          danger
          onConfirm={() => {
            setConfirming(undefined);
            onRemove();
          }}
          onCancel={() => setConfirming(undefined)}
        />
      )}
      {confirming !== undefined && confirming !== "remove" && (
        <ConfirmDialog
          title={`Take ${name} out of ${confirming.detach.name}?`}
          consequence="The library stops placing files here and no longer lists what is on it until it is added again. Nothing on disk is touched."
          confirmLabel="Take it out"
          danger
          onConfirm={() => {
            onDetachCategory(confirming.detach.id);
            setConfirming(undefined);
          }}
          onCancel={() => setConfirming(undefined)}
        />
      )}
    </li>
  );
}

function AttachCategory({
  alreadyAttached,
  categories,
  busy,
  onAttach,
}: {
  alreadyAttached: { id: number }[];
  categories: LibraryCategory[];
  busy: boolean;
  onAttach: (categoryId: number) => void;
}) {
  const libraryLabel = useLibraryLabel();
  const attachedIds = new Set(alreadyAttached.map((category) => category.id));
  const available = categories.filter((category) => !attachedIds.has(category.id));
  const [selected, setSelected] = useState("");

  if (available.length === 0) return null;

  return (
    <span className="flex items-center gap-1">
      <select
        value={selected}
        disabled={busy}
        onChange={(event) => setSelected(event.target.value)}
        className="rounded-full border border-dashed border-border-subtle bg-canvas
                   px-1.5 py-px text-[0.6875rem] text-ink-muted disabled:opacity-50"
      >
        <option value="">+ Add to library</option>
        {available.map((category) => (
          <option key={category.id} value={category.id}>
            {libraryLabel(category)}
          </option>
        ))}
      </select>
      {selected && (
        <button
          type="button"
          disabled={busy}
          onClick={() => {
            onAttach(Number(selected));
            setSelected("");
          }}
          className="rounded-full bg-accent px-1.5 py-px text-[0.6875rem] text-accent-ink disabled:opacity-50"
        >
          Add
        </button>
      )}
    </span>
  );
}

// A new root folder is useless until something attaches it to a category --
// found live 2026-09-11 against a genuinely fresh instance (notes.md), fixed
// first by adding the attach endpoint (previous commit) and now by folding
// the attach step into this same form: a root folder is added *for* a
// library, the way a folder in Sonarr/Radarr is added under a series/movie
// root. "Library" doubles as "category" (ADR-54: a library is a category);
// New library lets several libraries share one media_type (a Music library
// and a separate Soundtracks library, say) rather than forcing one category
// per media type.
function AddRootFolder({
  categories,
  onAdded,
}: {
  categories: LibraryCategory[];
  onAdded: () => void;
}) {
  const libraryLabel = useLibraryLabel();
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");
  const [libraryChoice, setLibraryChoice] = useState<string>(NEW_LIBRARY);
  const [libraryChoiceTouched, setLibraryChoiceTouched] = useState(false);
  const newLibrary = useNewLibrary();

  // categories arrives asynchronously (its own useQuery, one render behind
  // this form's first paint); once it does, default to the first real
  // library instead of leaving "+ New library" selected under someone who
  // already has libraries -- but only until they have touched the field
  // themselves.
  useEffect(() => {
    if (!libraryChoiceTouched && categories[0]) setLibraryChoice(String(categories[0].id));
  }, [categories, libraryChoiceTouched]);

  const create = useMutation({
    mutationFn: async () => {
      const { data: root, error: rootError } = await api.POST("/rootfolders", {
        body: { path, label: label || undefined },
      });
      if (rootError) throw rootError;

      let categoryId: number;
      if (libraryChoice === NEW_LIBRARY) {
        categoryId = (await newLibrary.create()).id;
      } else {
        categoryId = Number(libraryChoice);
      }

      const { error: attachError } = await api.POST("/categories/{id}/rootfolders", {
        params: { path: { id: categoryId } },
        body: { root_folder_id: root!.id },
      });
      if (attachError) throw attachError;
    },
    onSuccess: () => {
      setPath("");
      setLabel("");
      newLibrary.forgetName();
      onAdded();
    },
  });

  const creatingNewLibrary = libraryChoice === NEW_LIBRARY;
  const ready = path.trim() !== "";

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-48 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Path
        <input
          className={inputStyle}
          value={path}
          placeholder="/media/music"
          autoComplete="off"
          onChange={(event) => setPath(event.target.value)}
        />
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Label
        <input
          className={inputStyle}
          value={label}
          placeholder="music-hdd-1"
          autoComplete="off"
          onChange={(event) => setLabel(event.target.value)}
        />
      </label>
      <label className="flex min-w-40 flex-col gap-1 text-xs text-ink-muted">
        Library
        <select
          className={inputStyle}
          value={libraryChoice}
          onChange={(event) => {
            setLibraryChoiceTouched(true);
            setLibraryChoice(event.target.value);
          }}
        >
          {categories.map((category) => (
            <option key={category.id} value={category.id}>
              {libraryLabel(category)} ({mediaTypeName(category.media_type)})
            </option>
          ))}
          <option value={NEW_LIBRARY}>+ New library…</option>
        </select>
      </label>
      {creatingNewLibrary && <NewLibraryFields library={newLibrary} />}
      <Button
        variant="primary"
        type="submit"
        disabled={!ready || create.isPending}
      >
        <Plus aria-hidden className="size-3.5" />
        {create.isPending ? "Adding…" : "Add root folder"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That could not be added -- check the path exists and is mounted inside the container,
          and the library name is not empty.
        </p>
      )}
    </form>
  );
}

// A full scan that found a mass of this root's files gone marked none of them: a failed mount must not look like a
// deleted library. The person decides.
function VanishedHeld({ root }: { root: RootFolder }) {
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const accept = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/rootfolders/{id}/vanished/accept", { params: { path: { id: root.id } } }));
    },
    onSuccess: () => {
      setConfirming(false);
      void queryClient.invalidateQueries({ queryKey: ["rootfolders"] });
      void queryClient.invalidateQueries({ queryKey: ["system-health"] });
    },
  });
  const count = root.vanished_held ?? 0;
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-md border border-warn/40 bg-warn/10 p-2.5 text-sm">
      <p>
        The last full scan found <strong>{count.toLocaleString()}</strong> {count === 1 ? "file" : "files"} of this root
        gone and marked none of them missing. If the disk was not mounted, run a full scan once it is. If you deleted them,
        accept it: their albums and seasons are then missing and searched for again, unless the library unmonitors
        deleted files.
      </p>
      <div className="flex flex-wrap items-center gap-2">
        {confirming ? (
          <>
            <button
              type="button"
              onClick={() => accept.mutate()}
              disabled={accept.isPending}
              className="rounded-md bg-danger px-2.5 py-1 text-xs font-medium text-accent-ink disabled:opacity-50"
            >
              Mark {count.toLocaleString()} as gone
            </button>
            <button type="button" onClick={() => setConfirming(false)} className="text-xs text-ink-muted hover:text-ink">
              Cancel
            </button>
          </>
        ) : (
          <button
            type="button"
            onClick={() => setConfirming(true)}
            className="rounded-md border border-border-subtle px-2.5 py-1 text-xs hover:border-accent"
          >
            Accept as gone…
          </button>
        )}
        {accept.isError && <span className="text-xs text-danger">That was refused.</span>}
      </div>
    </div>
  );
}

function Badge({ tone, title, children }: { tone: "ok" | "warn"; title?: string; children: React.ReactNode }) {
  return (
    <span
      title={title}
      className={`rounded-full border px-1.5 py-px text-[0.6875rem] ${
        tone === "ok" ? "border-border-subtle text-ink-muted" : "border-warn text-warn"
      }`}
    >
      {children}
    </span>
  );
}

function formatBytes(bytes: number): string {
  if (bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(units.length - 1, Math.floor(Math.log(bytes) / Math.log(1024)));
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? Math.round(value) : value.toFixed(1)} ${units[exponent]}`;
}
