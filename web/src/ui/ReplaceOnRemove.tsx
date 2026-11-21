import { useState } from "react";

import { t } from "@/lib/i18n";

import { Button } from "./Button";

/** Shown under a remove that was refused because something still uses the row: the way forward is to move
 *  those uses to another row of the same kind, then remove. */
export function ReplaceOnRemove({
  name,
  reason,
  choices,
  pending,
  onReplace,
  onCancel,
}: {
  name: string;
  reason: string;
  choices: readonly { id: number; name: string }[];
  pending: boolean;
  onReplace: (id: number) => void;
  onCancel: () => void;
}) {
  const [chosen, setChosen] = useState<number | null>(choices[0]?.id ?? null);
  return (
    <div role="alert" className="flex flex-col gap-2 rounded-md border border-border-subtle bg-surface p-3 text-sm">
      <p className="text-danger">{reason}</p>
      {choices.length > 0 ? (
        <div className="flex flex-wrap items-center gap-2">
          <label className="flex items-center gap-2 text-ink-muted">
            {t("replace.move", { name })}
            <select
              value={chosen ?? ""}
              onChange={(event) => setChosen(Number(event.target.value))}
              className="rounded-md border border-border-subtle bg-surface px-2 py-1 text-ink"
            >
              {choices.map((choice) => (
                <option key={choice.id} value={choice.id}>
                  {choice.name}
                </option>
              ))}
            </select>
          </label>
          <Button variant="danger" disabled={pending || chosen === null} onClick={() => chosen !== null && onReplace(chosen)}>
            {t("replace.button", { name })}
          </Button>
        </div>
      ) : (
        <p className="text-ink-muted">{t("replace.none")}</p>
      )}
      <button type="button" onClick={onCancel} className="self-start text-xs text-ink-muted underline">
        {t("replace.keep", { name })}
      </button>
    </div>
  );
}
