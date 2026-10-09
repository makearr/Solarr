import type { ReactNode } from "react";

import { Button } from "./Button";
import { Dialog } from "./Dialog";

/** Replaces confirm(): names the action and its consequence, and the confirming button says what it does. */
export function ConfirmDialog({
  title,
  consequence,
  confirmLabel,
  cancelLabel = "Cancel",
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
  children,
}: {
  title: ReactNode;
  consequence: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
  /** Choices that change what confirming does, such as "also delete the files". */
  children?: ReactNode;
}) {
  return (
    <Dialog
      title={title}
      description={consequence}
      onClose={onCancel}
      actions={
        <>
          <Button variant="ghost" onClick={onCancel}>
            {cancelLabel}
          </Button>
          <Button variant={danger ? "danger" : "primary"} busy={busy} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children}
    </Dialog>
  );
}
