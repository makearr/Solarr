import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

// A convenience, not a gate (section 21.11): every step can be skipped.
export function Setup({ onDone }: { onDone: () => void }) {
  const status = useQuery({
    queryKey: ["setup"],
    queryFn: async () => {
      const { data, error } = await api.GET("/setup");
      if (error) throw error;
      return data;
    },
  });

  if (status.isPending) return <Centered>Checking what still needs doing…</Centered>;
  if (status.isError) return <Centered>Could not reach the server.</Centered>;

  return (
    <Centered>
      <div className="w-full max-w-md space-y-6">
        <header className="space-y-1">
          <h1 className="text-lg font-semibold tracking-tight">Set up MediaNexus</h1>
          <p className="text-sm text-ink-muted">
            Two steps. You can skip either and change it later.
          </p>
        </header>

        {status.data.needs_administrator ? (
          <CreateAdministrator />
        ) : (
          <AddRootFolder onSkip={onDone} onAdded={onDone} />
        )}
      </div>
    </Centered>
  );
}

function CreateAdministrator() {
  const queryClient = useQueryClient();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/setup/admin", {
        body: { username, password },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries(),
  });

  return (
    <Panel
      title="Step 1 — Create the administrator"
      description="The first account owns the install. Nobody else can claim it afterwards."
    >
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          create.mutate();
        }}
      >
        <Field label="Username">
          <input
            className={inputStyle}
            value={username}
            autoComplete="username"
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>
        <Field label="Password">
          <input
            className={inputStyle}
            type="password"
            value={password}
            autoComplete="new-password"
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>

        {create.isError && <Problem error={create.error} />}

        <button
          type="submit"
          disabled={!username.trim() || password.length < 12 || create.isPending}
          className={primaryButtonStyle}
        >
          {create.isPending ? "Creating…" : "Create account"}
        </button>
      </form>
    </Panel>
  );
}

function AddRootFolder({ onAdded, onSkip }: { onAdded: () => void; onSkip: () => void }) {
  const [path, setPath] = useState("");
  const [label, setLabel] = useState("");

  const add = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/rootfolders", {
        body: { path, label: label.trim() || undefined },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: onAdded,
  });

  return (
    <Panel
      title="Step 2 — Add a library folder"
      description="The path as the container sees it, not as your host sees it. It is checked before it is saved."
    >
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          add.mutate();
        }}
      >
        <Field label="Path">
          <input
            className={inputStyle}
            placeholder="/media/music"
            value={path}
            onChange={(event) => setPath(event.target.value)}
          />
        </Field>
        <Field label="Label" hint="Optional. Shown in the interface, e.g. music-hdd-1.">
          <input
            className={inputStyle}
            value={label}
            onChange={(event) => setLabel(event.target.value)}
          />
        </Field>

        {add.isError && <Problem error={add.error} />}

        <div className="flex gap-2">
          <button
            type="submit"
            disabled={!path.trim() || add.isPending}
            className={primaryButtonStyle}
          >
            {add.isPending ? "Checking…" : "Add folder"}
          </button>
          <button type="button" onClick={onSkip} className={quietButtonStyle}>
            Skip for now
          </button>
        </div>
      </form>
    </Panel>
  );
}

// `detail` is written to be read by a person; do not replace it.
function Problem({ error }: { error: unknown }) {
  const detail =
    typeof error === "object" && error !== null && "detail" in error
      ? String((error as { detail: unknown }).detail)
      : "The server rejected that.";

  return (
    <p role="alert" className="rounded-md border border-danger/40 bg-danger/10 p-2 text-sm text-danger">
      {detail}
    </p>
  );
}

function Panel({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-4 rounded-lg border border-border-subtle bg-surface p-5">
      <div className="space-y-1">
        <h2 className="text-sm font-medium">{title}</h2>
        <p className="text-xs text-ink-muted">{description}</p>
      </div>
      {children}
    </section>
  );
}

function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-ink-muted">{label}</span>
      {children}
      {hint && <span className="block text-xs text-ink-muted">{hint}</span>}
    </label>
  );
}

function Centered({ children }: { children: React.ReactNode }) {
  return <div className="flex h-full items-center justify-center p-6">{children}</div>;
}

const inputStyle =
  "w-full rounded-md border border-border-subtle bg-canvas px-2.5 py-1.5 text-sm " +
  "focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-accent";

const primaryButtonStyle =
  "rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink " +
  "disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 " +
  "focus-visible:outline-accent";

const quietButtonStyle =
  "rounded-md border border-border-subtle px-3 py-1.5 text-sm text-ink-muted " +
  "hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
