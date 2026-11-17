import { Trash2 } from "lucide-react";
import { useState, type ButtonHTMLAttributes } from "react";

import { Button } from "./Button";
import { ConfirmDialog } from "./ConfirmDialog";

export interface RemoveConfirmation {
  readonly title: string;
  /** What is lost, in words: a stored key that cannot be shown again, a grant that must be given anew. */
  readonly body: string;
}

/** A row's remove control: a bin that turns red on hover; aria-label says what goes. `confirm` is null only
 *  where the caller already asks, or where the row is a form entry not yet saved. */
export function RemoveButton({
  confirm,
  onClick,
  ...rest
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children" | "onClick"> & {
  onClick: () => void;
  "aria-label": string;
  confirm: RemoveConfirmation | null;
}) {
  const [asking, setAsking] = useState(false);
  return (
    <>
      <Button
        variant="remove"
        {...rest}
        onClick={() => (confirm === null ? onClick() : setAsking(true))}
      >
        <Trash2 aria-hidden className="size-3.5" />
      </Button>
      {asking && confirm !== null && (
        <ConfirmDialog
          title={confirm.title}
          consequence={confirm.body}
          confirmLabel="Remove"
          danger
          onCancel={() => setAsking(false)}
          onConfirm={() => {
            setAsking(false);
            onClick();
          }}
        />
      )}
    </>
  );
}
