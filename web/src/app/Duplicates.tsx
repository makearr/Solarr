import * as Dialog from "@radix-ui/react-dialog";
import { useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { type Vocabulary, vocabularyOf } from "@/lib/i18n";
import { invalidateWorkLists } from "@/lib/resources";
import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";
import { Button } from "@/ui/Button";

import { formatBytes } from "./Queue";

const skipReasons: Record<string, string> = {
  missing: "not found on disk",
  "size-changed": "changed since the scan",
  "other-filesystem": "on another filesystem",
  "no-method": "the filesystem allows neither a reflink nor a hardlink",
};

/** Identical copies a library holds at two paths can share one file. The plan is read first; the button queues the run,
 *  which compares every byte again before swapping a path for the link. */
function IdenticalCopies({ categoryId }: { categoryId: number }) {
  const queryClient = useQueryClient();
  const [queued, setQueued] = useState(false);
  const plan = useQuery({
    queryKey: ["links", categoryId],
    queryFn: () => ok(api.GET("/categories/{id}/links", { params: { path: { id: categoryId } } })),
  });
  const start = useMutation({
    mutationFn: async () => {
      await ok(api.POST("/categories/{id}/links", { params: { path: { id: categoryId } } }));
    },
    onSuccess: () => {
      setQueued(true);
      void queryClient.invalidateQueries({ queryKey: ["links", categoryId] });
    },
  });
  const data = plan.data;
  if (!data) return null;
  if (data.total === 0 && data.skipped.length === 0) return null;
  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
      <h2 className="text-sm font-medium">Identical files</h2>
      <p className="max-w-xl text-xs text-ink-muted">
        The same bytes stored at two paths of this library can be stored once: the second path becomes a reflink of the first
        where the filesystem offers it, otherwise a hardlink. Both paths keep working and stay in every folder they are in.
      </p>
      {data.total > 0 ? (
        <p className="text-sm">
          {data.total} {data.total === 1 ? "file" : "files"} can share bytes, freeing{" "}
          <span className="font-medium">{formatBytes(data.reclaimable_bytes)}</span>.
        </p>
      ) : (
        <p className="text-sm text-ink-muted">Nothing left to link.</p>
      )}
      {data.skipped.length > 0 && (
        <ul className="text-xs text-ink-muted">
          {data.skipped.map((skip) => (
            <li key={skip.reason}>
              {skip.count} left alone: {skipReasons[skip.reason] ?? skip.reason}
            </li>
          ))}
        </ul>
      )}
      {data.links.length > 0 && (
        <details className="text-xs">
          <summary className="cursor-pointer text-ink-muted">Show {data.links.length < data.total ? `the first ${data.links.length}` : "them"}</summary>
          <ul className="mt-1 flex flex-col gap-1">
            {data.links.map((link) => (
              <li key={link.replace} className="break-all font-mono text-ink-muted">
                {link.replace} → {link.keep} ({link.method})
              </li>
            ))}
          </ul>
        </details>
      )}
      {data.total > 0 && (
        <div className="flex items-center gap-3">
          <Button
            variant="primary"
            type="button"
            disabled={start.isPending || queued}
            onClick={() => start.mutate()}
          >
            {queued ? "Queued" : "Link identical files"}
          </Button>
          {start.isError && <span className="text-xs text-danger">Not queued: this needs manage access to the library.</span>}
          {queued && <span className="text-xs text-ink-muted">Running in the background; see Activity.</span>}
        </div>
      )}
    </section>
  );
}

type Group = components["schemas"]["DuplicateReport"]["groups"][number];
type Copy = Group["copies"][number];
type Action = "link" | "recycle";

const extensionOf = (path: string) => (path.match(/\.[^./\\]+$/)?.[0] ?? "").toLowerCase();

/** A copy can be linked to the kept one only when the kept copy has a file of the same kind to link to. */
function linkable(copy: Copy, keep: Copy): boolean {
  const kept = new Set(keep.paths.map(extensionOf));
  return copy.paths.every((path) => kept.has(extensionOf(path)));
}

/** One part held more than once: pick the copy to keep (the library's choice is ticked first), say what becomes of each
 *  other copy, and confirm. Nothing is removed without the dialog. */
function GroupCard({ group, categoryId, words, onDone }: { group: Group; categoryId: number; words: Vocabulary; onDone: () => void }) {
  const initialKeep = Math.max(0, group.copies.findIndex((copy) => copy.keep));
  const [keepIndex, setKeepIndex] = useState(initialKeep);
  const [actions, setActions] = useState<Record<number, Action>>({});
  const [confirming, setConfirming] = useState(false);
  const keep = (group.copies[keepIndex] ?? group.copies[0]) as Copy;
  const others = group.copies.filter((_, index) => index !== keepIndex);
  const actionFor = (copy: Copy): Action => {
    const chosen = actions[copy.media_object_id];
    if (chosen === "link" && !linkable(copy, keep)) return "recycle";
    return chosen ?? (linkable(copy, keep) ? "link" : "recycle");
  };
  const apply = useMutation({
    mutationFn: () => ok(api.POST("/categories/{id}/duplicates/resolve", {
      params: { path: { id: categoryId } },
      body: { keep_media_object_id: keep.media_object_id, drop: others.map((copy) => ({ media_object_id: copy.media_object_id, action: actionFor(copy) })) },
    })),
  });
  const freed = others.reduce((total, copy) => total + copy.size_bytes, 0);
  const done = apply.data;

  return (
    <li className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
        <span className="text-sm font-medium">{group.title}</span>
        <span className="text-xs text-ink-muted">{group.artist}</span>
        <span className="ml-auto text-xs text-ink-muted">frees up to {formatBytes(freed)}</span>
      </div>
      <ul className="flex flex-col gap-1.5">
        {group.copies.map((copy, index) => (
          <li
            key={copy.media_object_id}
            className={`flex flex-col gap-0.5 rounded-md border px-2.5 py-1.5 text-xs ${index === keepIndex ? "border-accent bg-accent/5" : "border-border-subtle"}`}
          >
            <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
              <label className="flex items-center gap-1.5">
                <input type="radio" name={`keep-${group.copies[0]?.media_object_id}`} checked={index === keepIndex} onChange={() => setKeepIndex(index)} />
                <span className="font-medium">{index === keepIndex ? "Keep this one" : "Keep"}</span>
              </label>
              <span className="rounded-full border border-border-subtle px-1.5 uppercase">{copy.audio_codec || "?"}</span>
              <span>{copy.lossless ? "lossless" : copy.bitrate_kbps > 0 ? `${copy.bitrate_kbps} kbit` : "bitrate unknown"}</span>
              <span className="text-ink-muted">{formatBytes(copy.size_bytes)}</span>
              <span className="text-ink-muted">{formatDuration(copy.duration_ms)}</span>
              <span className="text-ink-muted">on {copy.album}</span>
              {index !== keepIndex && (
                <label className="ml-auto flex items-center gap-1.5 text-ink-muted">
                  Instead
                  <select
                    value={actionFor(copy)}
                    onChange={(event) => setActions({ ...actions, [copy.media_object_id]: event.target.value as Action })}
                    className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-ink"
                  >
                    <option value="link" disabled={!linkable(copy, keep)}>
                      Link to the kept file (hardlink or reflink)
                    </option>
                    <option value="recycle">Move to the recycle bin</option>
                  </select>
                </label>
              )}
            </div>
            <span className="break-all font-mono text-ink-muted">{copy.paths.join("  =  ")}</span>
          </li>
        ))}
      </ul>
      <p className="text-xs text-ink-muted">Library's choice: {group.keep_reason}.</p>
      {done ? (
        <p className="text-xs text-ok">
          Done: {done.linked} linked, {done.recycled} recycled.
          {done.problems.map((problem) => (
            <span key={problem} className="block text-warn">
              {problem}
            </span>
          ))}
        </p>
      ) : (
        <div className="flex items-center gap-3">
          <button type="button" onClick={() => setConfirming(true)} className="rounded-md bg-accent px-3 py-1 text-xs text-accent-ink">
            Keep this copy, deal with the others…
          </button>
          {apply.isError && <span className="text-xs text-danger">That could not be done: it needs manage access to the library.</span>}
        </div>
      )}
      <Dialog.Root open={confirming} onOpenChange={setConfirming}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
          <Dialog.Content className="fixed left-1/2 top-1/2 z-50 flex w-[min(36rem,92vw)] -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-4">
            <Dialog.Title className="text-sm font-medium">
              Keep one copy of &ldquo;{group.title}&rdquo;?
            </Dialog.Title>
            <Dialog.Description className="text-xs text-ink-muted">
              Kept: {keep.paths[0]}
            </Dialog.Description>
            <ul className="flex flex-col gap-1 text-xs">
              {others.map((copy) => (
                <li key={copy.media_object_id} className="flex flex-col">
                  <span className={actionFor(copy) === "recycle" ? "font-medium text-warn" : "font-medium"}>
                    {actionFor(copy) === "recycle"
                      ? `Moved to the recycle bin (restorable for 7 days). The ${words.work} it is on will miss this ${words.part}.`
                      : "Every path becomes a link to the kept file; the old file waits in the recycle bin for 7 days."}
                  </span>
                  <span className="break-all font-mono text-ink-muted">{copy.paths.join("\n")}</span>
                </li>
              ))}
            </ul>
            <div className="flex justify-end gap-2">
              <Dialog.Close className="rounded-md border border-border-subtle px-3 py-1 text-xs text-ink-muted hover:text-ink">Cancel</Dialog.Close>
              <button
                type="button"
                disabled={apply.isPending}
                onClick={() =>
                  apply.mutate(undefined, {
                    onSuccess: () => {
                      setConfirming(false);
                      onDone();
                    },
                  })
                }
                className="rounded-md bg-accent px-3 py-1 text-xs text-accent-ink disabled:opacity-50"
              >
                {apply.isPending ? "Working…" : "Yes, do it"}
              </button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </li>
  );
}

/** Parts this library holds more than once, not found by their bytes: for music by artist, title and length, so a flac
 *  and a 96 kbit mp3 of one song are one track twice; otherwise two files on one part. Choose the copy to keep. */
/** How many groups are added each time the end of the list comes into view. */
const GROUPS_PER_STEP = 20;

/** Groups drawn a step at a time as the end comes into view; drawn cards stay mounted, so a choice or a result made in
 *  one survives scrolling. */
function GroupList({ groups, render }: { groups: Group[]; render: (group: Group) => React.ReactNode }) {
  const [shown, setShown] = useState(GROUPS_PER_STEP);
  const sentinel = useRef<HTMLLIElement>(null);
  useEffect(() => {
    const element = sentinel.current;
    if (!element) return;
    const observer = new IntersectionObserver((entries) => {
      if (entries.some((entry) => entry.isIntersecting)) setShown((count) => count + GROUPS_PER_STEP);
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [shown, groups.length]);
  return (
    <ul className="flex flex-col gap-2">
      {groups.slice(0, shown).map(render)}
      {shown < groups.length && <li ref={sentinel} className="h-8" aria-hidden />}
    </ul>
  );
}

export function Duplicates() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const library = useLibrary(categoryId);
  const info = useMediaTypes().find((type) => type.key === library?.media_type);
  const words = vocabularyOf(info);
  const queryClient = useQueryClient();
  const report = useQuery({
    queryKey: ["duplicates", categoryId],
    enabled: categoryId !== undefined,
    // Read afresh on every visit: a resolved group stays on screen as "Done", so the cached report still offers it.
    refetchOnMount: "always",
    queryFn: () => ok(api.GET("/categories/{id}/duplicates", { params: { path: { id: Number(categoryId) } } })),
  });
  const data = report.data;
  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4 p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Duplicates</h1>
        <p className="text-sm text-ink-muted">
          {info?.duplicates_by_name
            ? `The same recording stored more than once: the same artist and title, lengths within a few seconds, in any format and on any ${words.work}.`
            : `Two files on one ${words.part}.`}{" "}
          The library suggests the copy closer to its target quality; you decide. Giving a copy up either links its file to the
          kept one (so the other {words.work} keeps its {words.part}) or moves it to the recycle bin. Files the probe has not
          measured yet are not compared.
        </p>
      </header>
      {categoryId !== undefined && <IdenticalCopies categoryId={Number(categoryId)} />}
      {report.isLoading && <p className="text-sm text-ink-muted">Looking…</p>}
      {report.error !== null && !report.isLoading && <p className="text-sm text-danger">The report could not be made.</p>}
      {data && data.groups.length === 0 && <p className="text-sm text-ink-muted">No duplicate {words.parts} found.</p>}
      {data && data.groups.length > 0 && (
        <>
          <p className="text-sm">
            {data.limited
              ? `More than ${data.groups.length} ${words.parts} are held more than once; these ${data.groups.length} have the most to free, and the rest show once some are dealt with.`
              : `${data.groups.length} ${data.groups.length === 1 ? words.part : words.parts} held more than once.`}{" "}
            Dropping the extra copies shown would free <span className="font-medium">{formatBytes(data.reclaimable_bytes)}</span>.
          </p>
          <GroupList
            groups={data.groups}
            render={(group) => (
              <GroupCard
                key={group.copies.map((copy) => copy.media_object_id).join("-")}
                group={group}
                categoryId={Number(categoryId)}
                words={words}
                onDone={() => {
                  void queryClient.invalidateQueries({ queryKey: ["links", Number(categoryId)] });
                  // A recycled copy can leave a part missing.
                  invalidateWorkLists(queryClient);
                }}
              />
            )}
          />
        </>
      )}
    </div>
  );
}

function formatDuration(ms: number): string {
  const seconds = Math.round(ms / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}
