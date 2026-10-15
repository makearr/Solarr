import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import { ThemeToggle } from "./ThemeToggle";

/** Shown when there is an account but no session. Never shown when the server
 *  runs without authentication: /me answers there, so this is never reached. */
export function Login() {
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [remember, setRemember] = useState(false);

  const signIn = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/auth/login", {
        body: { username, password, remember },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries(),
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

          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            Username
            <input
              className={inputStyle}
              value={username}
              autoComplete="username"
              autoFocus
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>

          <label className="flex flex-col gap-1 text-xs text-ink-muted">
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
              {problemType(signIn.error) === "too-many-requests"
                ? "Too many failed sign-ins from this address. Wait a minute, then try again."
                : "The username or password is incorrect."}
            </p>
          )}

          <button
            type="submit"
            disabled={signIn.isPending || username === "" || password === ""}
            className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink
                       disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                       focus-visible:outline-accent"
          >
            {signIn.isPending ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </main>
    </div>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";

/** problemType reads an API problem's type slug, if the error is one. */
function problemType(error: unknown): string | undefined {
  return typeof error === "object" && error !== null && "type" in error && typeof error.type === "string" ? error.type : undefined;
}
