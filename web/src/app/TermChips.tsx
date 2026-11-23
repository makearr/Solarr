import { X } from "lucide-react";
import { useId, useState } from "react";

import { t } from "@/lib/i18n";
import { labelStyle } from "@/ui/form";

/** A list of words as chips: type one and press Enter or a comma, suggestions come from a list the caller knows. */
export function TermChips({ label, value, onChange, suggestions = [] }: { label: string; value: string[]; onChange: (next: string[]) => void; suggestions?: string[] }) {
  const [draft, setDraft] = useState("");
  const listId = useId();
  const add = (text: string) => {
    const term = text.trim();
    if (term !== "" && !value.some((one) => one.toLowerCase() === term.toLowerCase())) onChange([...value, term]);
    setDraft("");
  };
  return (
    <div className={labelStyle}>
      <span>{label}</span>
      <div className="flex flex-wrap items-center gap-1">
        {value.map((term) => (
          <span key={term} className="flex items-center gap-1 rounded-full border border-border-subtle px-2 py-0.5 text-ink">
            {term}
            <button
              type="button"
              aria-label={t("discover.removeTerm", { term, label })}
              onClick={() => onChange(value.filter((other) => other !== term))}
              className="text-ink-muted hover:text-ink"
            >
              <X aria-hidden className="size-3" />
            </button>
          </span>
        ))}
        <input
          aria-label={t("discover.addTerm", { label })}
          list={listId}
          value={draft}
          placeholder={t("discover.addTermPlaceholder")}
          onChange={(event) => {
            const text = event.target.value;
            if (text.endsWith(",")) add(text.slice(0, -1));
            else setDraft(text);
          }}
          onKeyDown={(event) => {
            if (event.key === "Enter") {
              event.preventDefault();
              add(draft);
            }
          }}
          onBlur={() => add(draft)}
          className="min-w-32 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink"
        />
        <datalist id={listId}>
          {suggestions.filter((one) => !value.includes(one)).map((one) => <option key={one} value={one} />)}
        </datalist>
      </div>
    </div>
  );
}
