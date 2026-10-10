import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HardDrive, Pencil, Plus, X } from "lucide-react";

import { api } from "@/api/client";
import { mediaTypeName } from "@/lib/mediaTypeIcon";
import type { components } from "@/api/schema";
import { EditCategoryForm } from "./Categories";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { MoveJobList, RootMoveControls } from "./Moves";

type RootFolder = components["schemas"]["RootFolder"];
type LibraryCategory = components["schemas"]["LibraryCategory"];
type MediaType = components["schemas"]["MediaType"];

const NEW_LIBRARY = "__new__";

/** todo 4.18: the root folder overview screen that never existed --
 *  internal/jobs/capacity has computed used/free/total for every root since
 *  2026-09-04 and nothing rendered it until now. Every number here is what
 *  the last capacity sweep found, not a live stat -- "capacity is polled,
 *  not assumed" (section 11.7). */
export function RootFolders() {
  const queryClient = useQueryClient();
  const roots = useQuery({
    queryKey: ["rootfolders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/rootfolders");
      if (error) throw error;
      return data;
    },
  });

  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["rootfolders"] });

  const attachCategory = useMutation({
    mutationFn: async (args: { rootFolderId: number; categoryId: number }) => {
      const { error } = await api.POST("/categories/{id}/rootfolders", {
        params: { path: { id: args.categoryId } },
        body: { root_folder_id: args.rootFolderId },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const detachCategory = useMutation({
    mutationFn: async (args: { rootFolderId: number; categoryId: number }) => {
      const { error } = await api.DELETE("/categories/{id}/rootfolders/{rootFolderId}", {
        params: { path: { id: args.categoryId, rootFolderId: args.rootFolderId } },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  // A root belongs to one library, so it leaves one before joining the next; a refused join puts it back.
  const moveRoot = useMutation({
    mutationFn: async (args: { rootFolderId: number; fromId: number; toId: number }) => {
      const { error: detachError } = await api.DELETE("/categories/{id}/rootfolders/{rootFolderId}", {
        params: { path: { id: args.fromId, rootFolderId: args.rootFolderId } },
      });
      if (detachError) throw detachError;
      const { error: attachError } = await api.POST("/categories/{id}/rootfolders", {
        params: { path: { id: args.toId } },
        body: { root_folder_id: args.rootFolderId },
      });
      if (attachError) {
        await api.POST("/categories/{id}/rootfolders", {
          params: { path: { id: args.fromId } },
          body: { root_folder_id: args.rootFolderId },
        });
        throw attachError;
      }
    },
    onSuccess: () => {
      invalidate();
      void queryClient.invalidateQueries({ queryKey: ["categories"] });
      void queryClient.invalidateQueries({ queryKey: ["libraries"] });
    },
  });

  const setPolicy = useMutation({
    mutationFn: async (args: { id: number; readOnly: boolean; allowRename: boolean; minFreeBytes: number }) => {
      const { error } = await api.PATCH("/rootfolders/{id}", {
        params: { path: { id: args.id } },
        body: { read_only: args.readOnly, allow_rename: args.allowRename, min_free_bytes: args.minFreeBytes },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  const setOnline = useMutation({
    mutationFn: async (args: { id: number; online: boolean }) => {
      const { error } = await api.PATCH("/rootfolders/{id}/online", {
        params: { path: { id: args.id } },
        body: { online: args.online },
      });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  // Refused (409) while any category still attaches this root -- detach it
  // from every category first (settings.ErrInUse, via the general handler).
  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/rootfolders/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Root folders</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Every library root, its own placement policy, and what the last capacity sweep found.
          A root declared offline stays that way until declared online again -- a disk coming
          back on its own never clears it.
        </p>
      </header>

      {roots.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {roots.error !== null && <p className="text-sm text-danger">Root folders could not be read.</p>}

      {roots.data && roots.data.length > 0 ? (
        <div className="flex flex-col gap-6">
          {groupByLibrary(roots.data, categories.data ?? []).map((group) => (
            <section key={group.key} className="flex flex-col gap-2">
              <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
                <h2 className="text-sm font-medium">{group.name}</h2>
                <span className="text-xs text-ink-muted">{libraryCapacity(group.roots)}</span>
              </div>
              <ul className="flex flex-col gap-3">
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
                    onMoveCategory={(fromId, toId) => moveRoot.mutate({ rootFolderId: root.id, fromId, toId })}
                    onLibraryEdited={() => {
                      invalidate();
                      void queryClient.invalidateQueries({ queryKey: ["categories"] });
                    }}
                    onRemove={() => remove.mutate(root.id)}
                    busy={setPolicy.isPending || setOnline.isPending}
                    categoryBusy={attachCategory.isPending || detachCategory.isPending || moveRoot.isPending}
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

      {(setPolicy.isError || setOnline.isError || moveRoot.isError) && (
        <p className="text-xs text-danger">That change was refused.</p>
      )}

      <AddRootFolder
        categories={categories.data ?? []}
        onAdded={() => {
          invalidate();
          void queryClient.invalidateQueries({ queryKey: ["categories"] });
        }}
      />

      <MoveJobList />
    </div>
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
  onMoveCategory,
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
  onMoveCategory: (fromId: number, toId: number) => void;
  onLibraryEdited: () => void;
  onRemove: () => void;
  busy: boolean;
  categoryBusy: boolean;
  removeBusy: boolean;
  removeError: unknown;
}) {
  const [readOnly, setReadOnly] = useState(root.read_only ?? false);
  const [allowRename, setAllowRename] = useState(root.allow_rename ?? false);
  const [minFreeBytes, setMinFreeBytes] = useState(root.min_free_bytes ?? 0);
  const [editingLibraryId, setEditingLibraryId] = useState<number | undefined>();
  const attached = root.categories ?? [];
  const onlyLibrary = attached.length === 1 ? attached[0] : undefined;
  const dirty =
    readOnly !== (root.read_only ?? false) ||
    allowRename !== (root.allow_rename ?? false) ||
    minFreeBytes !== (root.min_free_bytes ?? 0);

  const total = root.total_bytes ?? 0;
  const free = root.free_bytes ?? 0;
  const used = total > 0 ? total - free : 0;
  const usedPct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  const reservePct = total > 0 ? Math.min(100, Math.round(((root.min_free_bytes ?? 0) / total) * 100)) : 0;

  return (
    <li className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <HardDrive aria-hidden className="size-4 shrink-0 text-ink-muted" />
        <span className="font-medium">{root.label || root.path}</span>
        <span className="truncate font-mono text-xs text-ink-muted">{root.path}</span>

        <span className="ml-auto flex shrink-0 items-center gap-1.5">
          <Badge tone={root.online ? "ok" : "warn"}>{root.online ? "online" : "offline"}</Badge>
          {root.writable !== undefined && (
            <Badge tone={root.writable ? "ok" : "warn"}>{root.writable ? "writable" : root.read_only_detected ? "read-only filesystem" : "not writable"}</Badge>
          )}
          {root.drain_state && root.drain_state !== "NONE" && <Badge tone="warn">{root.drain_state}</Badge>}
        </span>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <span className="text-xs text-ink-muted">{attached.length === 1 ? "Library" : "Libraries"}</span>
        {attached.map((category) => (
          <span
            key={category.id}
            className={`flex items-center gap-1 rounded-full border py-px pl-2 pr-1 text-xs ${
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
              {category.name}
              <Pencil aria-hidden className="size-2.5 text-ink-muted" />
            </button>
            <button
              type="button"
              aria-label={`Remove this root folder from ${category.name}`}
              onClick={() => onDetachCategory(category.id)}
              disabled={categoryBusy}
              className="rounded-full p-0.5 text-ink-muted hover:text-danger disabled:opacity-50"
            >
              <X aria-hidden className="size-2.5" />
            </button>
          </span>
        ))}
        {onlyLibrary && (
          <MoveToLibrary
            from={onlyLibrary}
            categories={categories}
            busy={categoryBusy}
            onMove={(toId) => onMoveCategory(onlyLibrary.id, toId)}
          />
        )}
        {attached.length === 0 && (
          <AttachCategory
          alreadyAttached={attached}
          categories={categories}
          busy={categoryBusy}
          onAttach={onAttachCategory}
        />
        )}
      </div>
      {attached.length === 0 && (
        <p className="text-xs text-warn">
          Not in any library -- nothing will place files here until it is added to one.
        </p>
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

      {total > 0 ? (
        <div className="flex flex-col gap-1">
          <div className="relative h-2 overflow-hidden rounded-full bg-canvas">
            <div className="absolute inset-y-0 left-0 rounded-full bg-accent" style={{ width: `${usedPct}%` }} />
            {reservePct > 0 && (
              <div
                className="absolute inset-y-0 border-l-2 border-dashed border-danger/70"
                style={{ left: `${100 - reservePct}%` }}
              />
            )}
          </div>
          <span
            className="text-xs text-ink-muted"
            title={
              root.capacity_checked_at
                ? `${formatBytes(free)} free · measured ${new Date(root.capacity_checked_at).toLocaleString()}`
                : `${formatBytes(free)} free`
            }
          >
            {formatBytes(used)} used of {formatBytes(total)}
          </span>
        </div>
      ) : (
        <span className="text-xs text-ink-muted">Not measured yet -- the capacity sweep has not run.</span>
      )}

      <div className="flex flex-wrap items-center gap-3 border-t border-border-subtle pt-2.5 text-xs">
        <label className="flex items-center gap-1.5 text-ink-muted">
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
        <label className="flex items-center gap-1.5 text-ink-muted">
          <input
            type="checkbox"
            checked={allowRename}
            disabled={busy}
            onChange={(event) => setAllowRename(event.target.checked)}
            className="size-3.5"
          />
          allow rename
        </label>
        <label className="flex items-center gap-1.5 text-ink-muted">
          reserve
          <input
            type="number"
            min={0}
            value={Math.round(minFreeBytes / 1_000_000_000)}
            disabled={busy}
            onChange={(event) => setMinFreeBytes(Number(event.target.value) * 1_000_000_000)}
            className="w-16 rounded-md border border-border-subtle bg-canvas px-1.5 py-1 text-xs text-ink
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          />
          GB
        </label>

        {dirty && (
          <button
            type="button"
            onClick={() => onSavePolicy(readOnly, allowRename, minFreeBytes)}
            disabled={busy}
            className="rounded-md bg-accent px-2 py-1 text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            Save
          </button>
        )}

        <button
          type="button"
          onClick={onToggleOnline}
          disabled={busy}
          className="ml-auto rounded-md border border-border-subtle px-2 py-1 text-ink-muted
                     hover:text-ink disabled:opacity-50 focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {root.online ? "Declare offline" : "Declare online"}
        </button>

        <button
          type="button"
          onClick={() => {
            if (window.confirm(`Remove ${root.label || root.path}? This does not touch anything on disk.`)) {
              onRemove();
            }
          }}
          disabled={removeBusy}
          title={
            root.categories && root.categories.length > 0
              ? "Remove it from every library first"
              : "Remove this root folder"
          }
          className="rounded-md border border-border-subtle px-2 py-1 text-ink-muted
                     hover:border-danger hover:text-danger disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          Remove
        </button>
      </div>

      <RootMoveControls root={root} />

      {removeError !== null && (
        <p className="text-xs text-danger">
          {(removeError as { detail?: string }).detail ?? "That root folder could not be removed."}
        </p>
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
            {category.name}
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
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");
  const [libraryChoice, setLibraryChoice] = useState<string>(NEW_LIBRARY);
  const [libraryChoiceTouched, setLibraryChoiceTouched] = useState(false);
  const [newLibraryName, setNewLibraryName] = useState(mediaTypeName("music"));
  const [newLibraryNameTouched, setNewLibraryNameTouched] = useState(false);
  const [newLibraryMediaType, setNewLibraryMediaType] = useState<MediaType>("music");
  const mediaTypes = useMediaTypes();

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
        const { data: category, error: categoryError } = await api.POST("/categories", {
          body: {
            name: newLibraryName.trim() || mediaTypeName(newLibraryMediaType),
            media_type: newLibraryMediaType,
          },
        });
        if (categoryError) throw categoryError;
        categoryId = category!.id;
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
      setNewLibraryName(mediaTypeName(newLibraryMediaType));
      setNewLibraryNameTouched(false);
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
              {category.name} ({mediaTypeName(category.media_type)})
            </option>
          ))}
          <option value={NEW_LIBRARY}>+ New library…</option>
        </select>
      </label>
      {creatingNewLibrary && (
        <>
          <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
            Library name
            <input
              className={inputStyle}
              value={newLibraryName}
              placeholder={mediaTypeName(newLibraryMediaType)}
              autoComplete="off"
              onChange={(event) => {
                setNewLibraryName(event.target.value);
                setNewLibraryNameTouched(true);
              }}
            />
          </label>
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            Media type
            <select
              className={inputStyle}
              value={newLibraryMediaType}
              onChange={(event) => {
                const value = event.target.value as MediaType;
                setNewLibraryMediaType(value);
                if (!newLibraryNameTouched) setNewLibraryName(mediaTypeName(value));
              }}
            >
              {mediaTypes.map((type) => (
                <option key={type.key} value={type.key}>
                  {type.label}
                </option>
              ))}
            </select>
          </label>
        </>
      )}
      <button
        type="submit"
        disabled={!ready || create.isPending}
        className="flex items-center gap-1 rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                   disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                   focus-visible:outline-accent"
      >
        <Plus aria-hidden className="size-3.5" />
        {create.isPending ? "Adding…" : "Add root folder"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That could not be added -- check the path exists and is mounted inside the container,
          and the library name is not empty.
        </p>
      )}
    </form>
  );
}

function Badge({ tone, children }: { tone: "ok" | "warn"; children: React.ReactNode }) {
  return (
    <span
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

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

function MoveToLibrary({
  from,
  categories,
  busy,
  onMove,
}: {
  from: { id: number; name: string };
  categories: LibraryCategory[];
  busy: boolean;
  onMove: (toId: number) => void;
}) {
  const others = categories.filter((category) => category.id !== from.id);
  if (others.length === 0) return null;
  return (
    <select
      value=""
      disabled={busy}
      aria-label={`Move this root folder from ${from.name} to another library`}
      onChange={(event) => {
        const toId = Number(event.target.value);
        const to = others.find((category) => category.id === toId);
        if (to && window.confirm(`Move this root folder from ${from.name} to ${to.name}?`)) onMove(toId);
      }}
      className="rounded-full border border-dashed border-border-subtle bg-canvas
                 px-1.5 py-px text-[0.6875rem] text-ink-muted disabled:opacity-50"
    >
      <option value="">Move to…</option>
      {others.map((category) => (
        <option key={category.id} value={category.id}>
          {category.name}
        </option>
      ))}
    </select>
  );
}
