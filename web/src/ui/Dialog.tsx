import * as RadixDialog from "@radix-ui/react-dialog";
import type { ReactNode } from "react";
import { X } from "lucide-react";

const WIDTHS = { narrow: "w-[min(28rem,calc(100vw-2rem))]", wide: "w-[min(56rem,calc(100vw-2rem))]" };

/** A modal: title, an optional line saying what it is for, the body, and the actions at the foot. */
export function Dialog({
  title,
  description,
  width = "narrow",
  onClose,
  actions,
  closeLabel = "Close",
  children,
}: {
  title: ReactNode;
  description?: ReactNode;
  width?: keyof typeof WIDTHS;
  onClose: () => void;
  actions?: ReactNode;
  closeLabel?: string;
  children?: ReactNode;
}) {
  return (
    <RadixDialog.Root open onOpenChange={(open) => !open && onClose()}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className="fixed inset-0 z-40 bg-black/40" />
        <RadixDialog.Content
          aria-describedby={description ? undefined : ""}
          className={`fixed top-1/2 left-1/2 z-50 flex max-h-[85vh] ${WIDTHS[width]} -translate-x-1/2 -translate-y-1/2
                      flex-col gap-3 rounded-lg border border-border-subtle bg-canvas p-4 shadow-xl`}
        >
          <div className="flex items-start gap-2">
            <RadixDialog.Title className="text-sm font-medium">{title}</RadixDialog.Title>
            <RadixDialog.Close aria-label={closeLabel} className="ml-auto rounded-md p-1 text-ink-muted hover:text-ink">
              <X aria-hidden className="size-4" />
            </RadixDialog.Close>
          </div>
          {description && <RadixDialog.Description className="text-xs text-ink-muted">{description}</RadixDialog.Description>}
          {children && <div className="min-h-0 overflow-y-auto">{children}</div>}
          {actions && <div className="flex flex-wrap justify-end gap-2">{actions}</div>}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  );
}
