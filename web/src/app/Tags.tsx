import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";

import { api } from "@/api/client";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { Card } from "./SettingsCard";

/** The tags made from an album's Edit dialog: how many albums carry each, renamed or deleted here. A provider's
 *  genres are not listed; they are the provider's to change. */
export function Tags() {
  const queryClient = useQueryClient();
  const administrator = useIsAdministrator();
  const [editing, setEditing] = useState<{ id: number; name: string } | null>(null);

  const tags = useQuery({
    queryKey: ["tags", "own"],
    queryFn: async () => {
      const { data, error } = await api.GET("/tags/own");
      if (error) throw error;
      return data;
    },
  });
  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["tags"] });
    void queryClient.invalidateQueries({ queryKey: ["works"] });
  };
  const rename = useMutation({
    mutationFn: async (target: { id: number; name: string }) => {
      const { error, response } = await api.PUT("/tags/{id}", { params: { path: { id: target.id } }, body: { name: target.name } });
      if (error) throw new Error(response.status === 409 ? "taken" : "failed");
    },
    onSuccess: () => {
      setEditing(null);
      refresh();
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/tags/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: refresh,
  });

  const button = "rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted enabled:hover:text-ink disabled:opacity-50";
  return (
    <Card
      title="Every tag"
      help="Tags given in an Edit dialog. Deleting one takes it off everything it is on; nothing else is touched."
    >      {tags.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {tags.error !== null && !tags.isLoading && <p className="text-sm text-danger">The tags could not be read.</p>}
      {tags.data && tags.data.length === 0 && (
        <p className="text-sm text-ink-muted">No tags yet. Add one from an album's Edit dialog.</p>
      )}
      <ul className="flex max-w-2xl flex-col gap-1">
        {(tags.data ?? []).map((tag) => (
          <li key={tag.id} className="flex items-center gap-3 rounded-md border border-border-subtle bg-surface px-3 py-2 text-sm">
            {editing?.id === tag.id ? (
              <form
                className="flex flex-1 items-center gap-2"
                onSubmit={(event) => {
                  event.preventDefault();
                  rename.mutate(editing);
                }}
              >
                <input
                  autoFocus
                  value={editing.name}
                  onChange={(event) => setEditing({ id: tag.id, name: event.target.value })}
                  aria-label={`New name for ${tag.name}`}
                  className="min-w-0 flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-sm"
                />
                <button type="submit" disabled={rename.isPending || editing.name.trim() === ""} className={button}>
                  Save
                </button>
                <button type="button" onClick={() => setEditing(null)} className={button}>
                  Cancel
                </button>
              </form>
            ) : (
              <>
                <span className="min-w-0 flex-1 truncate font-medium">{tag.name}</span>
                <span className="shrink-0 text-xs tabular-nums text-ink-muted">
                  {tag.works} {tag.works === 1 ? "album" : "albums"}
                </span>
                {administrator && (
                  <>
                    <button type="button" onClick={() => { rename.reset(); setEditing({ id: tag.id, name: tag.name }); }} className={button}>
                      Rename
                    </button>
                    <button
                      type="button"
                      disabled={remove.isPending}
                      onClick={() => {
                        if (confirm(`Delete the tag "${tag.name}"? It comes off ${tag.works} ${tag.works === 1 ? "album" : "albums"}.`)) {
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
      {rename.isError && (
        <p className="text-xs text-danger">
          {rename.error.message === "taken" ? "Another tag already has that name." : "That could not be renamed."}
        </p>
      )}
      {remove.isError && <p className="text-xs text-danger">That could not be deleted.</p>}
    </Card>
  );
}
