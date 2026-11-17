import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import { Button } from "@/ui/Button";
import { inputStyle, labelStyle } from "@/ui/form";
import { ThemeToggle } from "./ThemeToggle";

/** Shown when there is an account but no session. Never shown when the server
 *  runs without authentication: /me answers there, so this is never reached. */
export function Login() {
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(false);

  const signIn = useMutation({
    mutationFn: () => ok(api.POST("/auth/login", {
      body: { username, password, remember },
    })),
    onSuccess: () => {
      // Another account's cached lists are never shown to the next person on a shared browser.
      queryClient.removeQueries({ predicate: (query) => query.queryKey.length !== 1 || query.queryKey[0] !== "me" });
      return queryClient.invalidateQueries({ queryKey: ["me"], exact: true });
    },
  });

  return (
    <div className="flex h-full flex-col">
      <header className="flex shrink-0 items-center justify-end px-4 py-3">
        <ThemeToggle />
      </header>

      <main className="flex min-h-0 flex-1 flex-col items-center justify-center gap-6 p-6">
        <div className="flex flex-col items-center gap-3">
          <img src="/favicon.svg" alt="" className="size-24" />
          <span className="text-2xl font-semibold tracking-tight">Solarr</span>
        </div>
        <form
          className="flex w-full max-w-sm flex-col gap-3 rounded-lg border border-border-subtle
                     bg-surface p-5"
          onSubmit={(event) => {
            event.preventDefault();
            signIn.mutate();
          }}
        >
          <h1 className="text-sm font-medium">Sign in</h1>

          <label className={labelStyle}>
            Username
            <input
              className={inputStyle}
              value={username}
              autoComplete="username"
              autoFocus
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>

          <label className={labelStyle}>
            Password
            <input
              className={inputStyle}
              type="password"
              value={password}
              autoComplete="current-password"
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>

          <label className="flex items-center gap-2 text-xs text-ink-muted">
            <input
              type="checkbox"
              checked={remember}
              onChange={(event) => setRemember(event.target.checked)}
              className="size-3.5 accent-[var(--color-accent)]"
            />
            Stay signed in
          </label>

          {signIn.isError && (
            <p className="text-xs text-danger">
              {signInFailure(signIn.error)}
            </p>
          )}

          <Button
            variant="primary"
            type="submit"
            disabled={signIn.isPending || username === "" || password === ""}
          >
            {signIn.isPending ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </main>
    </div>
  );
}

/** What a failed sign-in says; only a 401 may blame the password, or a retyped correct one feeds the rate limiter. */
export function signInFailure(error: unknown): string {
  const problem = error as { status?: unknown; type?: unknown } | null;
  const status = typeof problem?.status === "number" ? problem.status : 0;
  if (status === 401) return "The username or password is incorrect.";
  if (status === 429 || problem?.type === "too-many-requests") {
    return "Too many failed sign-ins from this address. Wait a minute, then try again.";
  }
  if (status === 400) return "The page and the server disagree about how to send the password; reload and try again.";
  return "The server could not be reached or had an error; the password was not checked.";
}
