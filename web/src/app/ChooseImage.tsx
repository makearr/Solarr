import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery } from "@tanstack/react-query";
import { X } from "lucide-react";
import { useState } from "react";

import { api } from "@/api/client";
import { useIsAdministrator } from "@/lib/useAdministrator";

import { bumpArtwork } from "./artworkVersion";

type Subject = "works" | "people";

/** Browse the pictures the providers hold for an album or artist and keep one (M1g 1.3). */
export function ChooseImage({ subject, id, title }: { subject: Subject; id: number; title: string }) {
  const [open, setOpen] = useState(false);
  if (!useIsAdministrator()) return null;
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted hover:text-ink"
      >
        Choose image
      </button>
      {open && <ImagePicker subject={subject} id={id} title={title} onClose={() => setOpen(false)} />}
    </>
  );
}

function ImagePicker({
  subject,
  id,
  title,
  onClose,
}: {
  subject: Subject;
  id: number;
  title: string;
  onClose: () => void;
}) {
  const options = useQuery({
    queryKey: [subject, id, "artwork-options"],
    queryFn: async () => {
      const path = { params: { path: { id } } };
      const { data, error } =
        subject === "works"
          ? await api.GET("/works/{id}/artwork/options", path)
          : await api.GET("/people/{id}/artwork/options", path);
      if (error) throw error;
      return data;
    },
    staleTime: 5 * 60_000,
    retry: false,
  });
  const choose = useMutation({
    mutationFn: async (url: string) => {
      const request = { params: { path: { id } }, body: { url } };
      const { error } =
        subject === "works"
          ? await api.PUT("/works/{id}/artwork", request)
          : await api.PUT("/people/{id}/artwork", request);
      if (error) throw error;
    },
    onSuccess: () => {
      bumpArtwork(subject, id);
      onClose();
    },
  });

  const list = options.data?.options ?? [];
  return (
    <Dialog.Root open onOpenChange={(next) => !next && onClose()}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <Dialog.Content
          className="fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] w-[min(56rem,calc(100vw-2rem))]
                     -translate-x-1/2 -translate-y-1/2 flex-col gap-3 rounded-lg border border-border-subtle
                     bg-canvas p-4 shadow-xl"
        >
          <div className="flex items-center gap-2">
            <Dialog.Title className="text-sm font-medium">Choose an image: {title}</Dialog.Title>
            <Dialog.Close aria-label="Close" className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </Dialog.Close>
          </div>
          <Dialog.Description className="text-xs text-ink-muted">
            What the metadata providers hold. The one you pick is kept over later refreshes.
          </Dialog.Description>
          {options.isLoading && <p className="text-sm text-ink-muted">Asking the providers…</p>}
          {options.error !== null && !options.isLoading && (
            <p className="text-sm text-danger">The providers could not be asked. Try again in a moment.</p>
          )}
          {options.data && list.length === 0 && (
            <p className="text-sm text-ink-muted">No provider has a picture for this.</p>
          )}
          {choose.error !== null && (
            <p className="text-sm text-danger">That image could not be fetched. Pick another.</p>
          )}
          <ul className="grid grid-cols-2 gap-3 overflow-y-auto sm:grid-cols-3 md:grid-cols-4">
            {list.map((option) => (
              <li key={option.url}>
                <button
                  type="button"
                  disabled={choose.isPending}
                  onClick={() => choose.mutate(option.url)}
                  className="flex w-full flex-col gap-1 rounded-md border border-border-subtle p-1.5 text-left
                             hover:border-accent disabled:opacity-50"
                >
                  <img
                    src={option.thumb_url}
                    alt={option.label}
                    loading="lazy"
                    referrerPolicy="no-referrer"
                    className="aspect-square w-full rounded object-cover"
                  />
                  <span className="text-xs">{option.label}</span>
                  <span className="text-xs text-ink-muted">{option.source}</span>
                </button>
              </li>
            ))}
          </ul>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
