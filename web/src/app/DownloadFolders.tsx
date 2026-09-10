import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Download, File, Folder, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type DownloadFolder = components["schemas"]["DownloadFolder"];

/** todo 4.18, section 5.1: the folders upstream download clients write to.
 *  A registry only -- nothing acts on what lands here until stage 5, and
 *  Contents below is a live, uncached listing, never a comparison against
 *  what MediaNexus has already imported. */
export function DownloadFolders() {
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState<number | null>(null);

  const folders = useQuery({
    queryKey: ["downloadfolders"],
    queryFn: async () => {
      const { data, error } = await api.GET("/downloadfolders");
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

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["downloadfolders"] });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/downloadfolders/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-4 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Download folders</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          Folders upstream download clients write to, watched from here -- never written to by
          MediaNexus. What acts on what lands here is stage 5; this is the registry alone.
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
              categoryName={categories.data?.find((category) => category.id === folder.category_id)?.name}
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
  categoryName,
  expanded,
  onToggleExpanded,
  onRemove,
  busy,
}: {
  folder: DownloadFolder;
  categoryName: string | undefined;
  expanded: boolean;
  onToggleExpanded: () => void;
  onRemove: () => void;
  busy: boolean;
}) {
  const contents = useQuery({
    queryKey: ["downloadfolders", folder.id, "contents"],
    enabled: expanded,
    queryFn: async () => {
      const { data, error } = await api.GET("/downloadfolders/{id}/contents", {
        params: { path: { id: folder.id } },
      });
      if (error) throw error;
      return data;
    },
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
        {categoryName && (
          <span className="rounded-full border border-border-subtle px-1.5 py-px text-[0.6875rem] text-ink-muted">
            {categoryName}
          </span>
        )}
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
      const { error } = await api.POST("/downloadfolders", {
        body: {
          path,
          label: label || undefined,
          category_id: categoryId ? Number(categoryId) : undefined,
        },
      });
      if (error) throw error;
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
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Category hint
        <select className={inputStyle} value={categoryId} onChange={(event) => setCategoryId(event.target.value)}>
          <option value="">None</option>
          {categories.map((category) => (
            <option key={category.id} value={category.id}>
              {category.name}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={!path.trim() || create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Adding…" : "Watch folder"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That path could not be used -- check it exists and is mounted inside the container.
        </p>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
