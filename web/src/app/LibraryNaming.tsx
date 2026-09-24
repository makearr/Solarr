import { useEffect, useRef, useState } from "react";
import { Link, useParams } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CircleHelp, X } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

import { useDebouncedValue } from "./useDebouncedValue";

type NamingSettings = components["schemas"]["NamingSettings"];
type NamingToken = components["schemas"]["NamingToken"];
type LibraryRenamePlan = components["schemas"]["LibraryRenamePlan"];
type ColonReplacement = NamingSettings["colon_replacement"];

// Lidarr's own labels for its colon replacement formats.
const COLON_REPLACEMENTS: readonly { value: ColonReplacement; label: string }[] = [
  { value: "delete", label: "Delete" },
  { value: "dash", label: "Replace with Dash" },
  { value: "space_dash", label: "Replace with Space Dash" },
  { value: "space_dash_space", label: "Replace with Space Dash Space" },
  { value: "smart", label: "Smart Replace" },
];

/** A library's naming, Lidarr's Track Naming section: the toggles, the three formats with a
 *  token builder behind each "?", live examples, presets, and "apply to library". */
export function LibraryNaming() {
  const { categoryId } = useParams({ strict: false }) as { categoryId?: string };
  const id = categoryId ? Number(categoryId) : undefined;
  const queryClient = useQueryClient();

  const naming = useQuery({
    queryKey: ["libraryNaming", id],
    enabled: id !== undefined,
    queryFn: async () => {
      const { data, error } = await api.GET("/categories/{id}/naming", { params: { path: { id: id! } } });
      if (error) throw error;
      return data;
    },
  });

  const [draft, setDraft] = useState<NamingSettings | undefined>();
  useEffect(() => {
    if (naming.data) setDraft(naming.data.naming);
  }, [naming.data]);

  const debouncedDraft = useDebouncedValue(draft, 250);
  const examples = useQuery({
    queryKey: ["libraryNaming", id, "examples", debouncedDraft],
    enabled: id !== undefined && debouncedDraft !== undefined,
    queryFn: async () => {
      const { data, error } = await api.POST("/categories/{id}/naming/examples", {
        params: { path: { id: id! } },
        body: debouncedDraft!,
      });
      if (error) throw error;
      return data;
    },
  });

  const save = useMutation({
    mutationFn: async (settings: NamingSettings) => {
      const { data, error } = await api.PUT("/categories/{id}/naming", {
        params: { path: { id: id! } },
        body: settings,
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => queryClient.setQueryData(["libraryNaming", id], data),
  });

  const [builderSlot, setBuilderSlot] = useState<string | undefined>();
  const inputs = useRef<Record<string, HTMLInputElement | null>>({});

  if (id === undefined) return <Message>No library in the URL.</Message>;
  if (naming.isLoading || !draft) return <Message>Loading…</Message>;
  if (naming.error !== null || !naming.data) return <Message>This library's naming could not be read.</Message>;

  const scheme = naming.data.scheme;
  const dirty = JSON.stringify(draft) !== JSON.stringify(naming.data.naming);
  const setFormat = (slot: string, template: string) =>
    setDraft({ ...draft, formats: { ...draft.formats, [slot]: template } });
  const preset = scheme.presets.find((candidate) =>
    scheme.slots.every((slot) => candidate.formats[slot.key] === draft.formats[slot.key]),
  );

  const insertToken = (slot: string, spelling: string) => {
    const input = inputs.current[slot];
    const current = draft.formats[slot] ?? "";
    const start = input?.selectionStart ?? current.length;
    const end = input?.selectionEnd ?? current.length;
    setFormat(slot, current.slice(0, start) + spelling + current.slice(end));
    requestAnimationFrame(() => {
      input?.focus();
      input?.setSelectionRange(start + spelling.length, start + spelling.length);
    });
  };

  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col gap-5 overflow-y-auto p-6">
      <header className="flex flex-col gap-1">
        <h1 className="text-lg font-semibold">Naming</h1>
        <p className="text-sm leading-relaxed text-ink-muted">
          How files in this library are named and where they go below the root folder.
          {naming.data.is_default && " This library uses the default until you save."}
        </p>
      </header>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <Toggle
          label="Rename Tracks"
          help="Off, a file keeps the name it arrived with; it still goes into the artist and album folders."
          checked={draft.rename_files}
          onChange={(checked) => setDraft({ ...draft, rename_files: checked })}
        />
        <Toggle
          label="Replace Illegal Characters"
          help='Replace characters some filesystems refuse ( / becomes +, ? becomes ! ). Off, they are removed.'
          checked={draft.replace_illegal_characters}
          onChange={(checked) => setDraft({ ...draft, replace_illegal_characters: checked })}
        />
        {draft.replace_illegal_characters && (
          <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span className="w-48 shrink-0">Colon Replacement</span>
            <select
              value={draft.colon_replacement}
              onChange={(event) =>
                setDraft({ ...draft, colon_replacement: event.target.value as ColonReplacement })
              }
              className={inputStyle}
            >
              {COLON_REPLACEMENTS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
            <span className="text-xs text-ink-muted">
              Applies to the whole name, so a colon you type in a format is replaced too.
            </span>
          </label>
        )}
      </section>

      <section className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4">
        <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          <span className="w-48 shrink-0">Preset</span>
          <select
            value={preset?.name ?? ""}
            onChange={(event) => {
              const chosen = scheme.presets.find((candidate) => candidate.name === event.target.value);
              if (chosen) setDraft({ ...draft, formats: { ...chosen.formats } });
            }}
            className={inputStyle}
          >
            {!preset && <option value="">Custom</option>}
            {scheme.presets.map((candidate) => (
              <option key={candidate.name} value={candidate.name}>
                {candidate.name}
              </option>
            ))}
          </select>
        </label>

        {scheme.slots.map((slot) => (
          <div key={slot.key} className="flex flex-col gap-1">
            <label htmlFor={`format-${slot.key}`} className="text-sm">
              {slot.label}
            </label>
            <div className="flex gap-2">
              <input
                id={`format-${slot.key}`}
                ref={(element) => {
                  inputs.current[slot.key] = element;
                }}
                value={draft.formats[slot.key] ?? ""}
                spellCheck={false}
                autoComplete="off"
                onChange={(event) => setFormat(slot.key, event.target.value)}
                className={`${inputStyle} min-w-0 flex-1 font-mono`}
              />
              <button
                type="button"
                aria-label={`Tokens for ${slot.label}`}
                title="Every token, explained"
                onClick={() => setBuilderSlot(slot.key)}
                className="shrink-0 rounded-md border border-border-subtle px-2 text-ink-muted hover:text-ink"
              >
                <CircleHelp aria-hidden className="size-4" />
              </button>
            </div>
            <p className="text-xs text-ink-muted">{slot.help}</p>
          </div>
        ))}

        <Examples
          examples={examples.data?.examples ?? naming.data.examples}
          problems={examples.data?.problems ?? []}
        />
      </section>

      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={!dirty || save.isPending || (examples.data?.problems.length ?? 0) > 0}
          onClick={() => save.mutate(draft)}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
        >
          {save.isPending ? "Saving…" : "Save"}
        </button>
        {dirty && (
          <button
            type="button"
            onClick={() => setDraft(naming.data.naming)}
            className="rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted hover:text-ink"
          >
            Discard changes
          </button>
        )}
        {save.isError && (
          <p className="text-xs text-danger">
            {(save.error as { detail?: string }).detail ?? "The naming could not be saved."}
          </p>
        )}
      </div>

      <ApplyToLibrary categoryId={id} unsaved={dirty} />

      {builderSlot !== undefined && (
        <TokenBuilder
          slotLabel={scheme.slots.find((slot) => slot.key === builderSlot)?.label ?? ""}
          tokens={scheme.tokens}
          onInsert={(spelling) => insertToken(builderSlot, spelling)}
          onClose={() => setBuilderSlot(undefined)}
        />
      )}
    </div>
  );
}

export function Toggle({
  label,
  help,
  checked,
  onChange,
}: {
  label: string;
  help: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
      <span className="w-48 shrink-0">{label}</span>
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      <span className="text-xs text-ink-muted">{help}</span>
    </label>
  );
}

function Examples({
  examples,
  problems,
}: {
  examples: readonly { label: string; path: string }[];
  problems: readonly string[];
}) {
  if (problems.length > 0) {
    return (
      <ul className="flex flex-col gap-0.5 rounded-md border border-danger/40 p-2 text-xs text-danger">
        {problems.map((problem) => (
          <li key={problem}>{problem}</li>
        ))}
      </ul>
    );
  }
  return (
    <dl className="flex flex-col gap-1 rounded-md bg-canvas p-2 text-xs">
      {examples.map((example) => (
        <div key={example.label} className="flex flex-col">
          <dt className="text-ink-muted">{example.label}</dt>
          <dd className="break-all font-mono">{example.path}</dd>
        </div>
      ))}
    </dl>
  );
}

// Lidarr's token builder options: a separator for words and a letter case, applied to the
// token as it is inserted -- {Artist.Name}, {ARTIST NAME}.
const SEPARATORS = [
  { value: " ", label: "Space" },
  { value: ".", label: "Period" },
  { value: "_", label: "Underscore" },
  { value: "-", label: "Dash" },
] as const;
const CASES = [
  { value: "default", label: "Default case" },
  { value: "upper", label: "Upper case" },
  { value: "lower", label: "Lower case" },
] as const;

function spell(token: string, separator: string, letterCase: string): string {
  const [name = "", spec] = token.slice(1, -1).split(":");
  // One-word tokens ({track:00}) are Lidarr's canonical spelling and are never re-spelled.
  if (!name.includes(" ")) return token;
  let spelled = name.replaceAll(" ", separator);
  if (letterCase === "upper") spelled = spelled.toUpperCase();
  if (letterCase === "lower") spelled = spelled.toLowerCase();
  return `{${spelled}${spec !== undefined ? `:${spec}` : ""}}`;
}

function TokenBuilder({
  slotLabel,
  tokens,
  onInsert,
  onClose,
}: {
  slotLabel: string;
  tokens: readonly NamingToken[];
  onInsert: (spelling: string) => void;
  onClose: () => void;
}) {
  const [separator, setSeparator] = useState<string>(" ");
  const [letterCase, setLetterCase] = useState<string>("default");

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const groups = [...new Set(tokens.map((token) => token.group))];

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 sm:p-10"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label={`${slotLabel} tokens`}
        onClick={(event) => event.stopPropagation()}
        className="flex w-full max-w-2xl flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-4 shadow-lg"
      >
        <div className="flex items-center gap-2">
          <h2 className="text-sm font-semibold">{slotLabel}</h2>
          <button
            type="button"
            aria-label="Close"
            onClick={onClose}
            className="ml-auto rounded p-1 text-ink-muted hover:text-ink"
          >
            <X aria-hidden className="size-4" />
          </button>
        </div>
        <p className="text-xs text-ink-muted">
          Click a token to insert it at the cursor. Text outside braces is written as typed, and a / starts
          a folder.
        </p>
        <div className="flex flex-wrap gap-2 text-xs">
          <select value={separator} onChange={(event) => setSeparator(event.target.value)} className={inputStyle}>
            {SEPARATORS.map((option) => (
              <option key={option.label} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
          <select value={letterCase} onChange={(event) => setLetterCase(event.target.value)} className={inputStyle}>
            {CASES.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </select>
        </div>

        {groups.map((group) => (
          <section key={group} className="flex flex-col gap-1">
            <h3 className="text-xs font-medium uppercase tracking-wide text-ink-muted">{group}</h3>
            <ul className="flex flex-col divide-y divide-border-subtle rounded-md border border-border-subtle">
              {tokens
                .filter((token) => token.group === group)
                .map((token) => {
                  const spelled = spell(token.spelling, separator, letterCase);
                  return (
                    <li key={token.spelling}>
                      <button
                        type="button"
                        onClick={() => onInsert(spelled)}
                        className="grid w-full grid-cols-[minmax(0,14rem)_minmax(0,9rem)_1fr] items-baseline gap-3
                                   px-2 py-1.5 text-left text-xs hover:bg-canvas"
                      >
                        <span className="truncate font-mono">{spelled}</span>
                        <span className="truncate font-mono text-ink-muted">{token.example}</span>
                        <span className="text-ink-muted">{token.help}</span>
                      </button>
                    </li>
                  );
                })}
            </ul>
          </section>
        ))}
      </div>
    </div>
  );
}

/** Lidarr's "apply to library": a preview of every file whose name would change, then the rename. */
function ApplyToLibrary({ categoryId, unsaved }: { categoryId: number; unsaved: boolean }) {
  const [plan, setPlan] = useState<LibraryRenamePlan | undefined>();
  const run = useMutation({
    mutationFn: async (preview: boolean) => {
      const { data, error } = await api.POST("/categories/{id}/rename", {
        params: { path: { id: categoryId } },
        body: { preview },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: (data) => setPlan(data),
  });

  const moves = plan?.roots.flatMap((root) => root.plan.moves) ?? [];
  const refused = plan?.roots.flatMap((root) => root.plan.refused) ?? [];
  const started = plan?.roots.some((root) => root.plan.accepted) ?? false;

  return (
    <section className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="text-sm font-medium">Apply to library</h2>
        <button
          type="button"
          disabled={unsaved || run.isPending}
          title={unsaved ? "Save the naming first" : "Show every file whose name would change"}
          onClick={() => run.mutate(true)}
          className="ml-auto rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted
                     hover:text-ink disabled:opacity-50"
        >
          {run.isPending && run.variables ? "Checking…" : "Preview renames"}
        </button>
      </div>
      {unsaved && <p className="text-xs text-ink-muted">Save the naming first; the library is renamed with what is saved.</p>}
      {run.isError && <p className="text-xs text-danger">The rename could not be planned.</p>}

      {plan && started && (
        <p className="text-sm">
          Renaming {moves.length} {moves.length === 1 ? "file" : "files"}. Progress is under{" "}
          <Link to="/storage/moves" className="text-accent hover:underline">
            Storage → Moves
          </Link>
          .
        </p>
      )}
      {plan && !started && moves.length === 0 && refused.length === 0 && (
        <p className="text-sm text-ink-muted">Every file already has the name this library would give it.</p>
      )}
      {plan && !started && moves.length > 0 && (
        <>
          <p className="text-sm">
            {moves.length} {moves.length === 1 ? "file" : "files"} would be renamed.
          </p>
          <ul className="max-h-80 overflow-y-auto rounded-md border border-border-subtle text-xs">
            {moves.map((move) => (
              <li key={move.media_object_id} className="flex flex-col border-b border-border-subtle px-2 py-1 last:border-0">
                <span className="break-all font-mono text-ink-muted line-through">{move.source_rel_path}</span>
                <span className="break-all font-mono">{move.dest_rel_path}</span>
              </li>
            ))}
          </ul>
          <div>
            <button
              type="button"
              disabled={run.isPending}
              onClick={() => run.mutate(false)}
              className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50"
            >
              {run.isPending && !run.variables ? "Starting…" : `Rename ${moves.length} ${moves.length === 1 ? "file" : "files"}`}
            </button>
          </div>
        </>
      )}
      {plan && refused.length > 0 && (
        <p className="text-xs text-warn">
          {refused.length} {refused.length === 1 ? "file is" : "files are"} left alone: {refused[0]?.reason}
        </p>
      )}
    </section>
  );
}

export function Message({ children }: { children: React.ReactNode }) {
  return <p className="p-6 text-sm text-ink-muted">{children}</p>;
}

export const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
