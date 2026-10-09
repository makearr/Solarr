import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, X } from "lucide-react";
import { useState } from "react";

import { api } from "@/api/client";

const iconButton =
  "flex size-7 items-center justify-center rounded-full border border-border-subtle bg-canvas/85 text-ink-muted shadow-sm hover:text-ink";

/** Radarr's Edit on an album (M1g 1.4): monitored, its own quality profile and the user's tags. */
export function EditAlbumButton({ id }: { id: number }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        title="Edit"
        aria-label="Edit"
        onClick={(event) => {
          event.preventDefault();
          event.stopPropagation();
          setOpen(true);
        }}
        className={iconButton}
      >
        <Pencil aria-hidden className="size-3.5" />
      </button>
      {open && <EditAlbumDialog id={id} onClose={() => setOpen(false)} />}
    </>
  );
}

function EditAlbumDialog({ id, onClose }: { id: number; onClose: () => void }) {
  const queryClient = useQueryClient();
  const edit = useQuery({
    queryKey: ["work-edit", id],
    queryFn: async () => {
      const { data, error } = await api.GET("/works/{id}/edit", { params: { path: { id } } });
      if (error) throw error;
      return data;
    },
  });
  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const known = useQuery({
    queryKey: ["tags"],
    queryFn: async () => {
      const { data, error } = await api.GET("/tags");
      if (error) throw error;
      return data;
    },
  });
  const [draft, setDraft] = useState<{ monitored: boolean; profileId: number | null; tags: string[] }>();
  const [typed, setTyped] = useState("");
  const form = draft ?? (edit.data ? { monitored: edit.data.monitored, profileId: edit.data.profile_id, tags: edit.data.tags } : undefined);

  const save = useMutation({
    mutationFn: async () => {
      if (!form) return;
      const pending = typed.trim();
      const tags = pending && !form.tags.includes(pending) ? [...form.tags, pending] : form.tags;
      const { error } = await api.PUT("/works/{id}/edit", {
        params: { path: { id } },
        body: { monitored: form.monitored, profile_id: form.profileId, tags },
      });
      if (error) throw error;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ predicate: (query) => String(query.queryKey[0]).startsWith("library-level") });
      void queryClient.invalidateQueries({ queryKey: ["tags"] });
      void queryClient.invalidateQueries({ queryKey: ["work-edit", id] });
      onClose();
    },
  });

  const addTag = () => {
    const name = typed.trim();
    if (form && name && !form.tags.some((tag) => tag.toLowerCase() === name.toLowerCase())) setDraft({ ...form, tags: [...form.tags, name] });
    setTyped("");
  };
  const suggestions = (known.data ?? []).filter(
    (name) => !form?.tags.some((tag) => tag.toLowerCase() === name.toLowerCase()),
  );

  return (
    <Dialog.Root open onOpenChange={(next) => !next && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          onClick={(event) => event.stopPropagation()}
          className="fixed top-1/2 left-1/2 z-50 flex w-[min(28rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2
                     flex-col gap-4 rounded-lg border border-border-subtle bg-canvas p-4 shadow-xl"
        >
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">Edit album</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">Monitoring, quality profile and tags of this album.</Dialog.Description>
          {edit.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
          {edit.error !== null && !edit.isLoading && <p className="text-sm text-danger">The album could not be read.</p>}
          {form && (
            <>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.monitored}
                  onChange={(event) => setDraft({ ...form, monitored: event.target.checked })}
                />
                Monitored
              </label>
              <label className="flex flex-col gap-1 text-sm">
                Quality profile
                <select
                  value={form.profileId ?? ""}
                  onChange={(event) => setDraft({ ...form, profileId: event.target.value ? Number(event.target.value) : null })}
                  className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm"
                >
                  <option value="">Library default</option>
                  {(profiles.data ?? [])
                    .filter((profile) => profile.media_type === "music")
                    .map((profile) => (
                      <option key={profile.id} value={profile.id}>
                        {profile.name}
                      </option>
                    ))}
                </select>
                <span className="text-xs text-ink-muted">Used instead of the library's profiles when this album is searched or upgraded.</span>
              </label>
              <div className="flex flex-col gap-1 text-sm">
                <label htmlFor="edit-album-tags">Tags</label>
                <ul className="flex flex-wrap gap-1">
                  {form.tags.map((tag) => (
                    <li key={tag} className="flex items-center gap-1 rounded-full border border-border-subtle px-2 py-0.5 text-xs">
                      {tag}
                      <button
                        type="button"
                        aria-label={`Remove tag ${tag}`}
                        onClick={() => setDraft({ ...form, tags: form.tags.filter((other) => other !== tag) })}
                        className="text-ink-muted hover:text-ink"
                      >
                        <X aria-hidden className="size-3" />
                      </button>
                    </li>
                  ))}
                </ul>
                <input
                  id="edit-album-tags"
                  list="edit-album-tag-names"
                  value={typed}
                  placeholder="Add a tag, then Enter"
                  onChange={(event) => setTyped(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      addTag();
                    }
                  }}
                  className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm"
                />
                <datalist id="edit-album-tag-names">
                  {suggestions.map((name) => (
                    <option key={name} value={name} />
                  ))}
                </datalist>
              </div>
            </>
          )}
          {save.error !== null && <p className="text-sm text-danger">That could not be saved.</p>}
          <div className="flex justify-end gap-2">
            <Dialog.Close className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">
              Cancel
            </Dialog.Close>
            <button
              type="button"
              disabled={!form || save.isPending}
              onClick={() => save.mutate()}
              className="rounded-md bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-50"
            >
              Save
            </button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

type Selection = { ids?: number[]; category_id?: number; excluded_ids?: number[]; fresh?: boolean; where?: string };
type Choice = "keep" | "yes" | "no";

/** Radarr's Edit selected: what the dialog sets is applied to every album of the selection, after the count is shown. */
export function BulkEditDialog({ selection, mediaType, onClose, onDone }: { selection: Selection; mediaType?: string; onClose: () => void; onDone: () => void }) {
  const [monitored, setMonitored] = useState<Choice>("keep");
  const [profile, setProfile] = useState<"keep" | "default" | number>("keep");
  const [addTags, setAddTags] = useState("");
  const [removeTags, setRemoveTags] = useState("");
  const [count, setCount] = useState<number | null>(null);
  const profiles = useQuery({
    queryKey: ["qualityProfiles"],
    queryFn: async () => {
      const { data, error } = await api.GET("/qualityprofiles");
      if (error) throw error;
      return data;
    },
  });
  const names = (raw: string) => raw.split(",").map((name) => name.trim()).filter((name) => name !== "");
  const send = useMutation({
    mutationFn: async (preview: boolean) => {
      const { data, error } = await api.POST("/works/bulk/edit", {
        body: {
          ...selection,
          preview,
          monitored: monitored === "keep" ? undefined : monitored === "yes",
          profile: profile === "keep" ? undefined : { id: profile === "default" ? null : profile },
          add_tags: names(addTags),
          remove_tags: names(removeTags),
        },
      });
      if (error) throw error;
      return { count: data.count, preview };
    },
    onSuccess: (result) => {
      if (result.preview) setCount(result.count);
      else onDone();
    },
  });
  const nothing =
    monitored === "keep" && profile === "keep" && names(addTags).length === 0 && names(removeTags).length === 0;
  const field = "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm";
  return (
    <Dialog.Root open onOpenChange={(next) => !next && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          className="fixed top-1/2 left-1/2 z-50 flex w-[min(28rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2
                     flex-col gap-4 rounded-lg border border-border-subtle bg-canvas p-4 shadow-xl"
        >
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">Edit selected albums</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-xs text-ink-muted">
            Only what you change is applied; the rest of each album stays as it is.
          </Dialog.Description>
          <label className="flex flex-col gap-1 text-sm">
            Monitored
            <select value={monitored} onChange={(event) => { setMonitored(event.target.value as Choice); setCount(null); }} className={field}>
              <option value="keep">No change</option>
              <option value="yes">Monitor</option>
              <option value="no">Unmonitor</option>
            </select>
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Quality profile
            <select
              value={String(profile)}
              onChange={(event) => {
                const value = event.target.value;
                setProfile(value === "keep" || value === "default" ? value : Number(value));
                setCount(null);
              }}
              className={field}
            >
              <option value="keep">No change</option>
              <option value="default">Library default</option>
              {(profiles.data ?? []).filter((one) => one.media_type === (mediaType ?? "music")).map((one) => (
                <option key={one.id} value={one.id}>{one.name}</option>
              ))}
            </select>
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Add tags
            <input value={addTags} onChange={(event) => { setAddTags(event.target.value); setCount(null); }} placeholder="comma separated" className={field} />
          </label>
          <label className="flex flex-col gap-1 text-sm">
            Remove tags
            <input value={removeTags} onChange={(event) => { setRemoveTags(event.target.value); setCount(null); }} placeholder="comma separated" className={field} />
          </label>
          {send.error !== null && <p className="text-sm text-danger">That could not be applied.</p>}
          <div className="flex items-center justify-end gap-2">
            {count !== null && <span className="mr-auto text-sm">Change {count} {count === 1 ? "album" : "albums"}?</span>}
            <Dialog.Close className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink">Cancel</Dialog.Close>
            {count === null ? (
              <button
                type="button"
                disabled={nothing || send.isPending}
                onClick={() => send.mutate(true)}
                className="rounded-md bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-50"
              >
                Review
              </button>
            ) : (
              <button
                type="button"
                disabled={send.isPending}
                onClick={() => send.mutate(false)}
                className="rounded-md bg-accent px-3 py-1.5 text-sm text-white disabled:opacity-50"
              >
                {send.isPending ? "Applying…" : "Apply"}
              </button>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
