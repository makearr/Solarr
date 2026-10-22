import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Download, File, Folder, Trash2 } from "lucide-react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useCategories, useDownloadFolders, useImportSettings } from "@/lib/resources";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";

import { TransferLadderEditor, type TransferMethod } from "./Import";

type DownloadFolder = components["schemas"]["DownloadFolder"];
type LibraryCategory = components["schemas"]["LibraryCategory"];

/** The folders upstream download clients write to (todo 4.18). Watched: every entry that has
 *  settled for ten minutes is imported once, into the folder's library, by its import method. */
export function DownloadFolders() {
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<number | null>(null);

  const folders = useDownloadFolders();

  const categories = useCategories();

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["downloadfolders"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await ok(api.DELETE("/downloadfolders/{id}", { params: { path: { id } } }));
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Download folders</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Folders your download clients write to. A watched folder is checked every five minutes:
          anything in it that has not changed for ten minutes is imported once into the library the folder
          names, and shows up under Import like any other import. A folder must name a library to be watched. Solarr only reads here, unless an import method lets it Move.
        </p>
      </header>

      {folders.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {folders.error !== null && <p className="text-sm text-danger">Download folders could not be read.</p>}

      {folders.data && folders.data.length > 0 ? (
        <ul className="flex flex-col gap-2">
          {folders.data.map((folder) => (
            <DownloadFolderCard
              key={folder.id}
              folder={folder}
              libraries={categories.data ?? []}
              expanded={expanded === folder.id}
              onToggleExpanded={() => setExpanded(expanded === folder.id ? null : folder.id)}
              onRemove={() => remove.mutate(folder.id)}
              busy={remove.isPending}
            />
          ))}
        </ul>
      ) : (
        !folders.isLoading && (
          <p className="rounded-lg border border-border-subtle bg-surface p-3 text-sm text-ink-muted">
            No download folder is configured.
          </p>
        )
      )}

      {remove.isError && <p className="text-xs text-danger">That folder could not be removed.</p>}

      <AddDownloadFolder categories={categories.data ?? []} onAdded={invalidate} />
    </div>
  );
}

function DownloadFolderCard({
  folder,
  libraries,
  expanded,
  onToggleExpanded,
  onRemove,
  busy,
}: {
  folder: DownloadFolder;
  libraries: LibraryCategory[];
  expanded: boolean;
  onToggleExpanded: () => void;
  onRemove: () => void;
  busy: boolean;
}) {
  const queryClient = useQueryClient();
  const importSettings = useImportSettings();
  const update = useMutation({
    mutationFn: async (change: Partial<Pick<DownloadFolder, "category_id" | "watch" | "transfer_ladder">>) => {
      const next = { ...folder, ...change };
      await ok(api.PATCH("/downloadfolders/{id}", {
        params: { path: { id: folder.id } },
        body: {
          label: next.label, category_id: next.category_id ?? undefined, watch: next.watch,
          transfer_ladder: next.transfer_ladder ?? undefined,
        },
      }));
    },
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: ["downloadfolders"] }),
  });
  const ownLadder = folder.transfer_ladder != null;

  const contents = useQuery({
    queryKey: ["downloadfolders", folder.id, "contents"],
    enabled: expanded,
    queryFn: () => ok(api.GET("/downloadfolders/{id}/contents", {
      params: { path: { id: folder.id } },
    })),
  });

  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-label={expanded ? "Collapse contents" : "Show contents"}
          onClick={onToggleExpanded}
          className="p-0.5 text-ink-muted hover:text-ink focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {expanded ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        </button>
        <Download aria-hidden className="size-4 shrink-0 text-ink-muted" />
        <span className="font-medium">{folder.label || folder.path}</span>
        <span className="truncate font-mono text-xs text-ink-muted">{folder.path}</span>
        {!folder.readable && (
          <span
            className="rounded-full border border-warn px-1.5 py-px text-[0.6875rem] text-warn"
            title={folder.readable_reason}
          >
            not readable
          </span>
        )}
        <button
          type="button"
          aria-label={`Stop watching ${folder.path}`}
          onClick={onRemove}
          disabled={busy}
          className="ml-auto shrink-0 rounded-md border border-border-subtle p-1 text-ink-muted
                     hover:text-danger disabled:opacity-50 focus-visible:outline-2
                     focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          <Trash2 aria-hidden className="size-3.5" />
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 text-xs">
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={folder.watch}
            disabled={update.isPending || (folder.category_id == null && !folder.watch)}
            title={folder.category_id == null ? "Choose the library this folder's downloads go into first" : undefined}
            onChange={(event) => update.mutate({ watch: event.target.checked })}
          />
          Watch for imports
        </label>
        <label className="flex items-center gap-1.5 text-ink-muted">
          Library
          <select
            value={folder.category_id ?? ""}
            disabled={update.isPending}
            onChange={(event) =>
              // Without a library nothing says where a download belongs, so watching stops with it.
              update.mutate(
                event.target.value === "" ? { category_id: undefined, watch: false } : { category_id: Number(event.target.value) },
              )
            }
            className={inputStyle}
          >
            <option value="">No library</option>
            {libraries.map((library) => (
              <option key={library.id} value={library.id}>
                {library.name}
              </option>
            ))}
          </select>
        </label>
        {folder.watch && folder.category_id == null && (
          <span className="text-warn">Nothing is imported from this folder until it names a library.</span>
        )}
        {update.isError && <span className="text-danger">The change was refused.</span>}
        <label className="flex items-center gap-1.5">
          <input
            type="checkbox"
            checked={ownLadder}
            disabled={update.isPending || !importSettings.data}
            onChange={(event) =>
              update.mutate({
                transfer_ladder: event.target.checked
                  ? ((importSettings.data?.transfer_ladder ?? []) as TransferMethod[])
                  : undefined,
              })
            }
          />
          Custom import method
          {!ownLadder && <span className="text-ink-muted">(unticked: the default import order)</span>}
        </label>
      </div>
      {ownLadder && (
        <TransferLadderEditor
          ladder={folder.transfer_ladder as TransferMethod[]}
          busy={update.isPending}
          failed={update.isError}
          onChange={(ladder) => update.mutate({ transfer_ladder: ladder })}
        />
      )}
      {update.isError && !ownLadder && <p className="text-xs text-danger">That could not be changed.</p>}

      {expanded && (
        <div className="border-t border-border-subtle pt-2">
          {contents.isLoading && <p className="text-xs text-ink-muted">Reading…</p>}
          {contents.error !== null && <p className="text-xs text-danger">Contents could not be read.</p>}
          {contents.data && contents.data.length === 0 && (
            <p className="text-xs text-ink-muted">Empty.</p>
          )}
          {contents.data && contents.data.length > 0 && (
            <ul className="flex flex-col gap-1">
              {contents.data.map((entry) => (
                <li key={entry.name} className="flex items-center gap-1.5 text-xs text-ink-muted">
                  {entry.is_dir ? (
                    <Folder aria-hidden className="size-3.5 shrink-0" />
                  ) : (
                    <File aria-hidden className="size-3.5 shrink-0" />
                  )}
                  <span className="truncate font-mono">{entry.name}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </li>
  );
}

function AddDownloadFolder({
  categories,
  onAdded,
}: {
  categories: readonly { id: number; name: string }[];
  onAdded: () => void;
}) {
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");
  const [categoryId, setCategoryId] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/downloadfolders", {
        body: {
          path,
          label: label || undefined,
          category_id: categoryId ? Number(categoryId) : undefined,
        },
      }));
    },
    onSuccess: () => {
      setPath("");
      setLabel("");
      setCategoryId("");
      onAdded();
    },
  });

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
          placeholder="/downloads/sabnzbd"
          autoComplete="off"
          onChange={(event) => setPath(event.target.value)}
        />
      </label>
      <label className="flex min-w-32 flex-col gap-1 text-xs text-ink-muted">
        Label
        <input
          className={inputStyle}
          value={label}
          placeholder="sabnzbd"
          autoComplete="off"
          onChange={(event) => setLabel(event.target.value)}
        />
      </label>
      <label className={labelStyle}>
        Library
        <select className={inputStyle} value={categoryId} onChange={(event) => setCategoryId(event.target.value)}>
          <option value="">All libraries</option>
          {categories.map((category) => (
            <option key={category.id} value={category.id}>
              {category.name}
            </option>
          ))}
        </select>
      </label>
      <Button
        variant="primary"
        type="submit"
        disabled={!path.trim() || create.isPending}
      >
        {create.isPending ? "Adding…" : "Watch folder"}
      </Button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That path could not be used -- check it exists and is mounted inside the container.
        </p>
      )}
    </form>
  );
}
