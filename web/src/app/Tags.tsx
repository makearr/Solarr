import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { useTagTree } from "@/lib/resources";
import { useFavoriteIds } from "@/lib/useFavorites";
import { useIsAdministrator } from "@/lib/useAdministrator";
import { useMediaTypes } from "@/lib/useMediaTypes";

import { HeartButton } from "@/ui/HeartButton";

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
  const search = useSearch({ strict: false }) as { tree?: string; tag?: number };
  const favorites = useFavoriteIds("tag");
  const [favoritesOnly, setFavoritesOnly] = useState(false);
  const tree = useTagTree();
  const genreTrees = [...new Map(useMediaTypes().filter((info) => info.genre_tree.key !== "").map((info) => [info.genre_tree.key, info.genre_tree.label]))];
  const genreTree = search.tree ?? genreTrees[0]?.[0] ?? "";
  const choose = (key: string) => void navigate({ to: ".", search: (previous: Record<string, unknown>) => ({ ...previous, tree: key, tag: undefined }) });
  const [editing, setEditing] = useState<{ id: number; name: string; parentId: number; mergeInto: number } | null>(null);
  const [adding, setAdding] = useState<{ name: string; parentId: number } | null>(null);

  const all = tree.data ?? [];
  const rows = orderedTags(all, genreTree).filter(({ tag }) => tag.genre_tree === genreTree && (!favoritesOnly || favorites.has(tag.id)));
  const openTag = all.find((tag) => tag.id === search.tag);
  const open = (id: number | undefined) => void navigate({ to: ".", search: (previous: Record<string, unknown>) => ({ ...previous, tag: id }) });
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
    // The tag dropdown counts a library's tags under a key of its own.
    void queryClient.invalidateQueries({ queryKey: ["library-tags"] });
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

  const block = useMutation({
    mutationFn: async (id: number) => {
      const { error, response } = await api.POST("/tags/{id}/block", { params: { path: { id } } });
      if (error) throw failure(response.status);
    },
    onSuccess: () => {
      refresh();
      void queryClient.invalidateQueries({ queryKey: ["blocked-tags"] });
    },
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
      help="Genres are tags in a tree per kind of media: choosing Rock in a library's Tags filter finds Metal too. Providers' genres land in it by name or alias; yours are never removed by a refresh. Your tags are labels of your own outside any tree. A refresh adds tags to an item but never removes one; delete a tag here to remove it. Press a tag to see its items, the heart to make it a favorite."
    >
      <div className="flex flex-wrap items-center gap-1" role="tablist" aria-label="Genre tree">
        {[...genreTrees, ["", "Your tags"] as [string, string]].map(([key, label]) => (
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
        <label className="ml-auto flex items-center gap-1 text-xs text-ink-muted">
          <input type="checkbox" checked={favoritesOnly} onChange={(event) => setFavoritesOnly(event.target.checked)} />
          Favorites only
        </label>
        {administrator && (
          <button type="button" className={button} onClick={() => { create.reset(); setAdding({ name: "", parentId: 0 }); }}>
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
        <p className="text-sm text-ink-muted">{genreTree === "" ? favoritesOnly ? "No favorite tags here." : "No tags of your own yet. Add one here or from an Edit dialog." : "No genres yet; a refresh brings the providers'."}</p>
      )}
      <ul className="flex max-w-3xl flex-col rounded-md border border-border-subtle">
        {rows.map(({ tag, depth }) => (
          <li key={tag.id} style={{ paddingLeft: `${0.5 + depth * 1.25}rem` }} className="flex items-center gap-3 border-b border-border-subtle px-2 py-0.5 text-sm last:border-0 hover:bg-surface">
            {editing?.id === tag.id ? (
              editRow(tag)
            ) : (
              <>
                <HeartButton subject="tag" id={tag.id} name={tag.name} />
                <button
                  type="button"
                  onClick={() => open(openTag?.id === tag.id ? undefined : tag.id)}
                  aria-pressed={openTag?.id === tag.id}
                  className={`min-w-0 flex-1 truncate text-left hover:underline ${depth === 0 ? "font-medium" : ""} ${openTag?.id === tag.id ? "text-accent" : ""}`}
                  title={tag.aliases.length > 0 ? `Also known as: ${tag.aliases.join(", ")}` : "Show the items with this tag"}
                >
                  {tag.name}
                </button>
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
                    <button
                      type="button"
                      disabled={block.isPending}
                      title="Delete it and never let it come back, not from a provider and not from anyone's edit"
                      onClick={() => {
                        if (confirm(`Block the tag "${tag.name}"? It comes off ${tag.works} ${tag.works === 1 ? "item" : "items"} and is not created again until you unblock it below.`)) {
                          block.mutate(tag.id);
                        }
                      }}
                      className={button}
                    >
                      Block
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
      {block.isError && <p className="text-xs text-danger">That could not be blocked.</p>}
      {administrator && <BlockedTags />}
      {openTag && <TagItems tag={openTag} />}
    </Card>
  );
}

/** Every item, in any library the caller can read, that carries the tag or one below it. */
function TagItems({ tag }: { tag: TagNode }) {
  const items = useInfiniteQuery({
    queryKey: ["tag-items", tag.id],
    initialPageParam: "",
    queryFn: ({ pageParam }) => ok(api.GET("/works", { params: { query: { tags: String(tag.id), limit: 100, ...(pageParam ? { cursor: pageParam } : {}) } } })),
    getNextPageParam: (page) => page.next_cursor || undefined,
  });
  const rows = items.data?.pages.flatMap((page) => page.items) ?? [];
  return (
    <section className="flex max-w-3xl flex-col gap-1" aria-label={`Items tagged ${tag.name}`}>
      <h3 className="text-sm font-medium">Items tagged {tag.name}</h3>
      {items.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {items.isError && <p className="text-sm text-danger">The items could not be read.</p>}
      {items.isSuccess && rows.length === 0 && <p className="text-sm text-ink-muted">Nothing carries this tag yet.</p>}
      <ul className="flex flex-col rounded-md border border-border-subtle">
        {rows.map((work) => (
          <li key={work.id} className="flex items-center gap-3 border-b border-border-subtle px-2 py-0.5 text-sm last:border-0">
            <Link to="/works/$id" params={{ id: String(work.id) }} className="min-w-0 flex-1 truncate hover:underline">
              {work.title}
            </Link>
            {work.year != null && <span className="shrink-0 text-xs text-ink-muted">{work.year}</span>}
          </li>
        ))}
      </ul>
      {items.hasNextPage && (
        <button type="button" className={`self-start ${button}`} disabled={items.isFetchingNextPage} onClick={() => void items.fetchNextPage()}>
          Show more
        </button>
      )}
    </section>
  );
}

/** The tags an administrator blocked: gone from every item and never created again until unblocked here. */
function BlockedTags() {
  const queryClient = useQueryClient();
  const blocked = useQuery({ queryKey: ["blocked-tags"], queryFn: () => ok(api.GET("/tags/blocked")) });
  const unblock = useMutation({
    mutationFn: async (tag: components["schemas"]["BlockedTag"]) => {
      const { error } = await api.DELETE("/tags/blocked", { params: { query: { name: tag.name, genre_tree: tag.genre_tree } } });
      if (error) throw new Error("failed");
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["blocked-tags"] }),
  });
  const rows = blocked.data ?? [];
  return (
    <section className="flex max-w-3xl flex-col gap-1" aria-label="Blocked tags">
      <h3 className="text-sm font-medium">Blocked tags</h3>
      {blocked.isSuccess && rows.length === 0 && <p className="text-sm text-ink-muted">No tag is blocked. Block puts a tag here.</p>}
      <ul className="flex flex-col rounded-md border border-border-subtle">
        {rows.map((tag) => (
          <li key={`${tag.genre_tree}/${tag.name}`} className="flex items-center gap-3 border-b border-border-subtle px-2 py-0.5 text-sm last:border-0">
            <span className="min-w-0 flex-1 truncate">{tag.name}</span>
            <span className="shrink-0 text-xs text-ink-muted">{tag.genre_tree === "" ? "your tags" : tag.genre_tree}</span>
            <button type="button" disabled={unblock.isPending} onClick={() => unblock.mutate(tag)} className={button}>
              Unblock
            </button>
          </li>
        ))}
      </ul>
      {unblock.isError && <p className="text-xs text-danger">That could not be unblocked.</p>}
    </section>
  );
}
