import { t, type MessageKey } from "@/lib/i18n";
import { setOverlay, useOverlay } from "@/lib/overlays";
import { SHORTCUTS } from "@/lib/shortcuts";
import { Dialog } from "@/ui/Dialog";

/** "?" lists every shortcut. */
export function ShortcutsHelp() {
  if (useOverlay() !== "shortcuts") return null;
  return (
    <Dialog title={t("shortcuts.title")} onClose={() => setOverlay(null)} closeLabel={t("shortcuts.close")}>
      <dl className="grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-sm">
        {SHORTCUTS.map((shortcut) => (
          <div key={shortcut.label} className="contents">
            <dt><kbd className="rounded border border-border-subtle px-1.5 py-0.5 font-mono text-xs">{shortcut.keys}</kbd></dt>
            <dd>{t(`shortcuts.${shortcut.label}` as MessageKey)}</dd>
          </div>
        ))}
      </dl>
    </Dialog>
  );
}
