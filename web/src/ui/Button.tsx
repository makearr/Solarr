import { Trash2 } from "lucide-react";
import type { ButtonHTMLAttributes, ReactNode } from "react";

export type ButtonVariant = "primary" | "secondary" | "danger" | "ghost" | "icon" | "remove";

const VARIANTS: Record<ButtonVariant, string> = {
  primary: "border border-accent bg-accent px-3 py-1.5 text-accent-ink enabled:hover:opacity-90",
  secondary: "border border-border-subtle px-3 py-1.5 text-ink enabled:hover:border-accent enabled:hover:text-accent",
  danger: "border border-danger px-3 py-1.5 text-danger enabled:hover:bg-danger enabled:hover:text-accent-ink",
  ghost: "px-2 py-1 text-ink-muted enabled:hover:text-ink",
  icon: "p-1 text-ink-muted enabled:hover:text-ink",
  remove: "border border-border-subtle p-1 text-ink-muted enabled:hover:text-danger",
};

/** The one button: a variant, and while busy a label saying so and no second click. */
export function Button({
  variant = "secondary",
  busy = false,
  busyLabel,
  icon,
  children,
  className,
  disabled,
  type = "button",
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  busy?: boolean;
  /** Shown instead of the label while busy; the label itself when absent. */
  busyLabel?: string;
  icon?: ReactNode;
}) {
  return (
    <button
      type={type}
      disabled={disabled || busy}
      aria-busy={busy || undefined}
      className={`inline-flex items-center justify-center gap-1.5 rounded-md text-sm focus-visible:outline-2
                  focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-50 ${VARIANTS[variant]} ${className ?? ""}`}
      {...rest}
    >
      {icon}
      {busy && busyLabel ? busyLabel : children}
    </button>
  );
}

/** A row's remove control: a bin that turns red on hover; aria-label says what goes. */
export function RemoveButton(props: Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children"> & { "aria-label": string }) {
  return (
    <Button variant="remove" {...props}>
      <Trash2 aria-hidden className="size-3.5" />
    </Button>
  );
}
