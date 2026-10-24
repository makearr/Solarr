import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { useTagTree } from "@/lib/resources";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useMediaTypes } from "@/lib/useMediaTypes";

import { Card } from "./SettingsCard";
import { orderedTags } from "./TagChips";

type TagNode = components["schemas"]["TagNode"];

const button = "rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted enabled:hover:text-ink disabled:opacity-50";
const field = "rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm";

/** Where a tag came from, in a person's words. */
function sourceLabel(source: string): string {
  if (source === "user") return "yours";
  if (source === "shipped") return "shipped";
  return `from ${source}`;
}

/** Every tag as a tree per genre tree, plus the free labels: renamed, moved under another, merged, added or
 *  deleted here. A provider's genre renamed or merged keeps its old name as an alias, so a refresh follows. */
export function Tags() {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const navigate = useNavigate();
  const search = useSearch({ strict: false }) as { tree?: string };
  const tree = useTagTree();
  const genreTrees = [...new Map(useMediaTypes().filter((info) => info.genre_tree.key !== "").map((info) => [info.genre_tree.key, info.genre_tree.label]))];
  const genreTree = search.tree ?? genreTrees[0]?.[0] ?? "";
  const choose = (key: string) => void navigate({ to: ".", search: (previous: Record<string, unknown>) => ({ ...previous, tree: key }) });
  const [editing, setEditing] = useState<{ id: number; name: string; parentId: number; mergeInto: number } | null>(null);
  const [adding, setAdding] = useState<{ name: string; parentId: number } | null>(null);

  const all = tree.data ?? [];
  const rows = orderedTags(all, genreTree).filter(({ tag }) => tag.genre_tree === genreTree);
  const inGenreTree = all.filter((tag) => tag.genre_tree === genreTree);
  // A tag may not go under itself or anything below it.
  const below = (id: number): Set<number> => {
    const out = new Set([id]);
    for (let grew = true; grew; ) {
      grew = false;
      for (const tag of inGenreTree) {
        if (tag.parent_id && out.has(tag.parent_id) && !out.has(tag.id)) {
          out.add(tag.id);
          grew = true;
        }
      }
    }
    return out;
  };

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["tags"] });
    void queryClient.invalidateQueries({ queryKey: ["works"] });
  };
  const failure = (status: number) => new Error(status === 409 ? "conflict" : "failed");
  const save = useMutation({
    mutationFn: async (target: NonNullable<typeof editing>) => {
      const original = all.find((tag) => tag.id === target.id);
      if (target.mergeInto) {
        const { error, response } = await api.POST("/tags/{id}/merge", { params: { path: { id: target.id } }, body: { into_id: target.mergeInto } });
        if (error) throw failure(response.status);
        return;
      }
      if (original && original.name === target.name.trim() && (original.parent_id ?? 0) === target.parentId) return;
      const { error, response } = await api.PUT("/tags/{id}", {
        params: { path: { id: target.id } },
        body: { name: target.name, parent_id: target.parentId || null },
      });
      if (error) throw failure(response.status);
    },
    onSuccess: () => {
      setEditing(null);
      refresh();
    },
  });
  const create = useMutation({
    mutationFn: async (target: NonNullable<typeof adding>) => {
      const { error, response } = await api.POST("/tags", { body: { name: target.name, genre_tree: genreTree, parent_id: target.parentId || null } });
      if (error) throw failure(response.status);
    },
    onSuccess: () => {
      setAdding(null);
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error, response } = await api.DELETE("/tags/{id}", { params: { path: { id } } });
      if (error) throw failure(response.status);
    },
    onSuccess: refresh,
  });

  const parentChoices = (exclude: Set<number>) =>
    orderedTags(inGenreTree, genreTree)
      .filter(({ tag }) => tag.genre_tree === genreTree && !exclude.has(tag.id))
      .map(({ tag, depth }) => (
        <option key={tag.id} value={tag.id}>
          {"  ".repeat(depth)}
          {tag.name}
        </option>
      ));

  const editRow = (tag: TagNode) =>
    editing && (
      <form
        className="flex flex-1 flex-wrap items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          save.mutate(editing);
        }}
      >
        <input autoFocus value={editing.name} onChange={(event) => setEditing({ ...editing, name: event.target.value })} aria-label={`New name for ${tag.name}`} className={`w-56 ${field}`} />
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          under
          <select value={editing.parentId} onChange={(event) => setEditing({ ...editing, parentId: Number(event.target.value) })} className={field}>
            <option value={0}>(top)</option>
            {parentChoices(below(tag.id))}
          </select>
        </label>
        <label className="flex items-center gap-1 text-xs text-ink-muted">
          or merge into
          <select value={editing.mergeInto} onChange={(event) => setEditing({ ...editing, mergeInto: Number(event.target.value) })} className={field}>
            <option value={0}>(no merge)</option>
            {parentChoices(below(tag.id))}
          </select>
        </label>
        <button type="submit" disabled={save.isPending || editing.name.trim() === ""} className={button}>
          {editing.mergeInto ? "Merge" : "Save"}
        </button>
        <button type="button" onClick={() => setEditing(null)} className={button}>
          Cancel
        </button>
      </form>
    );

  return (
    <Card
      title="Tags"
      help="Genres are tags in a tree per kind of media: choosing Rock in a library's Tags filter finds Metal too. Providers' genres land in it by name or alias; yours are never removed by a refresh. Free labels are tags of your own outside any tree."
    >
      <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label="Genre tree">
        {[...genreTrees, ["", "Free labels"] as [string, string]].map(([key, label]) => (
          <button
            key={key || "free"}
            type="button"
            role="tab"
            aria-selected={key === genreTree}
            onClick={() => choose(key)}
            className={`rounded-md border px-3 py-1 text-sm ${key === genreTree ? "border-accent text-ink" : "border-border-subtle text-ink-muted"}`}
          >
            {label}
          </button>
        ))}
        {administrator && (
          <button type="button" className={`ml-auto ${button}`} onClick={() => { create.reset(); setAdding({ name: "", parentId: 0 }); }}>
            Add tag
          </button>
        )}
      </div>
      {adding && (
        <form
          className="flex max-w-2xl flex-wrap items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            create.mutate(adding);
          }}
        >
          <input autoFocus value={adding.name} onChange={(event) => setAdding({ ...adding, name: event.target.value })} aria-label="New tag name" placeholder="Name" className={`min-w-0 flex-1 ${field}`} />
          {genreTree !== "" && (
            <select value={adding.parentId} onChange={(event) => setAdding({ ...adding, parentId: Number(event.target.value) })} aria-label="Under" className={field}>
              <option value={0}>(top)</option>
              {parentChoices(new Set())}
            </select>
          )}
          <button type="submit" disabled={create.isPending || adding.name.trim() === ""} className={button}>
            Add
          </button>
          <button type="button" onClick={() => setAdding(null)} className={button}>
            Cancel
          </button>
        </form>
      )}
      {tree.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {tree.error !== null && !tree.isLoading && <p className="text-sm text-danger">The tags could not be read.</p>}
      {tree.data && rows.length === 0 && (
        <p className="text-sm text-ink-muted">{genreTree === "" ? "No labels yet. Add one here or from an Edit dialog." : "No genres yet; a refresh brings the providers'."}</p>
      )}
      <ul className="flex max-w-3xl flex-col rounded-md border border-border-subtle">
        {rows.map(({ tag, depth }) => (
          <li key={tag.id} style={{ paddingLeft: `${0.5 + depth * 1.25}rem` }} className="flex items-center gap-3 border-b border-border-subtle px-2 py-0.5 text-sm last:border-0 hover:bg-surface">
            {editing?.id === tag.id ? (
              editRow(tag)
            ) : (
              <>
                <span className={`min-w-0 flex-1 truncate ${depth === 0 ? "font-medium" : ""}`} title={tag.aliases.length > 0 ? `Also known as: ${tag.aliases.join(", ")}` : undefined}>
                  {tag.name}
                </span>
                <span className="shrink-0 text-xs text-ink-muted">{sourceLabel(tag.source)}</span>
                <span className="w-16 shrink-0 text-right text-xs tabular-nums text-ink-muted">{tag.works} {tag.works === 1 ? "item" : "items"}</span>
                {administrator && (
                  <>
                    <button type="button" onClick={() => { save.reset(); setEditing({ id: tag.id, name: tag.name, parentId: tag.parent_id ?? 0, mergeInto: 0 }); }} className={button}>
                      Edit
                    </button>
                    <button
                      type="button"
                      disabled={remove.isPending}
                      onClick={() => {
                        const back = tag.source === "user" || tag.source === "shipped" ? "" : " A provider's genre comes back with the next refresh; merge it instead to keep it away.";
                        if (confirm(`Delete the tag "${tag.name}"? It comes off ${tag.works} ${tag.works === 1 ? "item" : "items"}; tags below it move up.${back}`)) {
                          remove.mutate(tag.id);
                        }
                      }}
                      className={button}
                    >
                      Delete
                    </button>
                  </>
                )}
              </>
            )}
          </li>
        ))}
      </ul>
      {(save.isError || create.isError) && (
        <p className="text-xs text-danger">
          {(save.error ?? create.error)?.message === "conflict" ? "Another tag already has that name, or it cannot go there." : "That could not be saved."}
        </p>
      )}
      {remove.isError && <p className="text-xs text-danger">That could not be deleted.</p>}
    </Card>
  );
}
