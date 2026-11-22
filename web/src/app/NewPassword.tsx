import { t } from "@/lib/i18n";
export const MIN_PASSWORD_LENGTH = 8;

/** A new password is ready to send when it is long enough and typed twice the same. */
export function passwordReady(password: string, repeat: string): boolean {
  return [...password].length >= MIN_PASSWORD_LENGTH && password === repeat;
}

/** The second password field, with the rule beside it: a typo here would lock the person out of their own account. */
export function RepeatPassword({
  password,
  repeat,
  onRepeat,
  inputClassName,
}: {
  password: string;
  repeat: string;
  onRepeat: (value: string) => void;
  inputClassName: string;
}) {
  const differs = repeat !== "" && repeat !== password;
  return (
    <div className="flex flex-col gap-1">
      <input
        className={inputClassName}
        type="password"
        value={repeat}
        autoComplete="new-password"
        placeholder={t("password.repeat")}
        aria-label={t("password.repeat")}
        aria-invalid={differs}
        onChange={(event) => onRepeat(event.target.value)}
      />
      <span className={differs ? "text-xs text-danger" : "text-xs text-ink-muted"}>
        {differs ? "The passwords differ" : `At least ${MIN_PASSWORD_LENGTH} characters.`}
      </span>
    </div>
  );
}
