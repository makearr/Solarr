import { CircleAlert, CircleCheck, Info, X } from "lucide-react";

import { t } from "@/lib/i18n";
import { dismissToast, useToasts, type Toast } from "@/lib/toast";

const ICON = { success: CircleCheck, error: CircleAlert, info: Info } as const;
const TONE = { success: "text-accent", error: "text-danger", info: "text-ink-muted" } as const;

/** Every toast, bottom right, above the status bar. Errors are announced at once, the rest politely. */
export function Toaster() {
  const toasts = useToasts();
  if (toasts.length === 0) return null;
  return (
    <div className="pointer-events-none fixed bottom-12 right-4 z-50 flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2">
      {toasts.map((toast) => <ToastItem key={toast.id} toast={toast} />)}
    </div>
  );
}

function ToastItem({ toast }: { toast: Toast }) {
  const Icon = ICON[toast.tone];
  return (
    <div
      role={toast.tone === "error" ? "alert" : "status"}
      className="pointer-events-auto flex items-start gap-2 rounded-md border border-border-subtle bg-surface p-3 text-sm shadow-lg"
    >
      <Icon aria-hidden className={`mt-0.5 size-4 shrink-0 ${TONE[toast.tone]}`} />
      <div className="min-w-0 flex-1">
        <p>{toast.message}</p>
        {toast.detail && <p className="mt-0.5 break-words text-xs text-ink-muted">{toast.detail}</p>}
      </div>
      {toast.action && (
        <button
          type="button"
          onClick={() => {
            toast.action?.run();
            dismissToast(toast.id);
          }}
          className="shrink-0 rounded-md px-2 py-0.5 text-accent hover:bg-canvas focus-visible:outline-2 focus-visible:outline-accent"
        >
          {toast.action.label}
        </button>
      )}
      <button
        type="button"
        aria-label={t("toast.dismiss")}
        onClick={() => dismissToast(toast.id)}
        className="shrink-0 rounded-md p-0.5 text-ink-muted hover:text-ink focus-visible:outline-2 focus-visible:outline-accent"
      >
        <X aria-hidden className="size-3.5" />
      </button>
    </div>
  );
}
