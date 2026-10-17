const LETTERS = ["#", ..."abcdefghijklmnopqrstuvwxyz"];

/** An A-Z strip beside a long list: a letter starts the list there, "#" at the beginning. */
export function LetterRail({ value, onChange }: { value: string; onChange: (letter: string) => void }) {
  return (
    <nav aria-label="Jump to a letter" className="absolute inset-y-2 right-1 z-10 flex w-5 flex-col items-center justify-between rounded-full bg-surface/80 py-1 text-[10px] leading-none text-ink-muted backdrop-blur">
      {LETTERS.map((letter) => {
        const current = (letter === "#" ? "" : letter) === value;
        return (
          <button
            key={letter}
            type="button"
            aria-label={letter === "#" ? "From the start" : `From ${letter.toUpperCase()}`}
            aria-current={current ? "true" : undefined}
            onClick={() => onChange(letter === "#" ? "" : letter)}
            className={`w-full rounded px-0.5 py-px hover:text-ink focus-visible:outline-2 focus-visible:outline-accent ${current ? "font-bold text-accent" : ""}`}
          >
            {letter === "#" ? "#" : letter.toUpperCase()}
          </button>
        );
      })}
    </nav>
  );
}
