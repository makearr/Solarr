import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { t } from "@/lib/i18n";

import { RepeatPassword, passwordReady } from "../NewPassword";
import { Field, Panel, Problem, inputStyle, primaryButtonStyle } from "./ui";

/** The one step with no Back and no Skip: nothing else works without an account. */
export function AccountStep() {
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [repeat, setRepeat] = useState("");
  const [setupCode, setSetupCode] = useState("");

  const create = useMutation({
    meta: { handlesErrors: true },
    mutationFn: () =>
      ok(api.POST("/setup/admin", { body: { username, password, ...(setupCode.trim() ? { setup_code: setupCode.trim() } : {}) } })),
    onSuccess: () =>
      Promise.all([queryClient.invalidateQueries({ queryKey: ["setup"] }), queryClient.invalidateQueries({ queryKey: ["me"] })]),
  });

  const codeRequired = (create.error as { type?: string } | null)?.type === "setup-code-required" || setupCode !== "";

  return (
    <Panel title={t("setup.account.title")} description={t("setup.account.description")}>
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label={t("setup.account.username")}>
          <input className={inputStyle} value={username} autoComplete="username" onChange={(event) => setUsername(event.target.value)} />
        </Field>
        <Field label={t("setup.account.password")}>
          <input className={inputStyle} type="password" value={password} autoComplete="new-password" onChange={(event) => setPassword(event.target.value)} />
        </Field>
        <RepeatPassword password={password} repeat={repeat} onRepeat={setRepeat} inputClassName={inputStyle} />
        {codeRequired && (
          <Field label={t("setup.account.code")} hint={t("setup.account.codeHint")}>
            <input className={inputStyle} value={setupCode} autoComplete="off" placeholder="xxxx-xxxx-xxxx" onChange={(event) => setSetupCode(event.target.value)} />
          </Field>
        )}
        {create.isError && <Problem error={create.error} />}
        <button type="submit" disabled={!username.trim() || !passwordReady(password, repeat) || create.isPending} className={primaryButtonStyle}>
          {create.isPending ? t("setup.account.creating") : t("setup.account.create")}
        </button>
      </form>
    </Panel>
  );
}
