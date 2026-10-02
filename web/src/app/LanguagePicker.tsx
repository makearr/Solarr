import { X } from "lucide-react";

import { LANGUAGES, languageName } from "@/lib/languages";

/** A set of languages chosen from a list: the chosen ones as chips, the rest in a drop-down. */
export function LanguagePicker({
  label,
  value,
  onChange,
  help,
}: {
  label: string;
  value: string[];
  onChange: (next: string[]) => void;
  help?: string;
}) {
  const remaining = LANGUAGES.filter((language) => !value.includes(language.code));
  return (
    <div className="flex flex-col gap-1 text-xs text-ink-muted">
      <span>{label}</span>
      <div className="flex flex-wrap items-center gap-1">
        {value.map((code) => (
          <span key={code} className="flex items-center gap-1 rounded-full border border-border-subtle px-2 py-0.5 text-ink">
            {languageName(code)}
            <button
              type="button"
              aria-label={`Remove ${languageName(code)} from ${label}`}
              onClick={() => onChange(value.filter((other) => other !== code))}
              className="text-ink-muted hover:text-ink"
            >
              <X aria-hidden className="size-3" />
            </button>
          </span>
        ))}
        <select
          aria-label={`Add a language to ${label}`}
          value=""
          onChange={(event) => event.target.value && onChange([...value, event.target.value])}
          className="rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs text-ink"
        >
          <option value="">Add…</option>
          {remaining.map((language) => (
            <option key={language.code} value={language.code}>
              {language.name}
            </option>
          ))}
        </select>
      </div>
      {help && <span>{help}</span>}
    </div>
  );
}
