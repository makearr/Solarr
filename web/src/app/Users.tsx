import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Trash2 } from "lucide-react";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type UserAccount = components["schemas"]["UserAccount"];
type Role = components["schemas"]["Role"];

const ROLES: readonly { id: Role; label: string; blurb: string }[] = [
  { id: "admin", label: "Administrator", blurb: "Changes settings, indexers and downloaders." },
  { id: "operator", label: "Operator", blurb: "Works the queue; libraries are granted per category." },
  { id: "readonly", label: "Read only", blurb: "Looks, and requests where allowed." },
];

/** Accounts and what they may do. Per-category grants are the other half
 *  (ADR-27) and appear once categories exist to grant on. */
export function Users({ currentUserId }: { currentUserId: number }) {
  const queryClient = useQueryClient();
  const users = useQuery({
    queryKey: ["users"],
    queryFn: async () => {
      const { data, error } = await api.GET("/users");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["users"] });

  const update = useMutation({
    mutationFn: async (change: { id: number; role?: Role; password?: string }) => {
      const { data, error } = await api.PATCH("/users/{id}", {
        params: { path: { id: change.id } },
        body: { role: change.role, password: change.password },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/users/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  // Only administrators can read this list at all, so a 403 means the viewer is
  // not one rather than that something broke.
  if (users.error !== null) return null;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Users</h2>

      <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {(users.data ?? []).map((user) => (
          <Row
            key={user.id}
            user={user}
            isSelf={user.id === currentUserId}
            onRole={(role) => update.mutate({ id: user.id, role })}
            onPassword={(password) => update.mutate({ id: user.id, password })}
            onRemove={() => remove.mutate(user.id)}
            busy={update.isPending || remove.isPending}
          />
        ))}
      </ul>

      {(update.isError || remove.isError) && (
        <p className="text-xs text-danger">
          That change was refused. The last administrator cannot be removed or demoted.
        </p>
      )}

      <AddUser onAdded={invalidate} />
    </section>
  );
}

function Row({
  user,
  isSelf,
  onRole,
  onPassword,
  onRemove,
  busy,
}: {
  user: UserAccount;
  isSelf: boolean;
  onRole: (role: Role) => void;
  onPassword: (password: string) => void;
  onRemove: () => void;
  busy: boolean;
}) {
  const [password, setPassword] = useState("");
  const [resetting, setResetting] = useState(false);

  return (
    <li className="flex flex-col gap-2 px-3 py-2.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{user.username}</span>
        {isSelf && <span className="text-[0.6875rem] text-ink-muted">(you)</span>}
        {!user.has_password && (
          <span className="rounded-full border border-warn px-1.5 py-px text-[0.6875rem] text-warn">
            no password
          </span>
        )}

        <select
          aria-label={`Role for ${user.username}`}
          value={user.role}
          disabled={busy}
          onChange={(event) => onRole(event.target.value as Role)}
          className="ml-auto rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs"
        >
          {ROLES.map((role) => (
            <option key={role.id} value={role.id}>
              {role.label}
            </option>
          ))}
        </select>

        <button
          type="button"
          onClick={() => setResetting(!resetting)}
          className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          Password
        </button>

        <button
          type="button"
          aria-label={`Remove ${user.username}`}
          onClick={onRemove}
          disabled={busy}
          className="rounded-md border border-border-subtle p-1 text-ink-muted hover:text-danger
                     disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          <Trash2 aria-hidden className="size-3.5" />
        </button>
      </div>

      {resetting && (
        <form
          className="flex items-center gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            onPassword(password);
            setPassword("");
            setResetting(false);
          }}
        >
          <input
            type="password"
            value={password}
            autoComplete="new-password"
            placeholder="New password"
            onChange={(event) => setPassword(event.target.value)}
            className="flex-1 rounded-md border border-border-subtle bg-canvas px-2 py-1 text-xs"
          />
          <button
            type="submit"
            disabled={password === ""}
            className="rounded-md border border-border-subtle px-2 py-1 text-xs text-ink-muted
                       hover:text-ink disabled:opacity-50"
          >
            Set
          </button>
        </form>
      )}

      <p className="text-xs text-ink-muted">
        {ROLES.find((role) => role.id === user.role)?.blurb}
      </p>
    </li>
  );
}

function AddUser({ onAdded }: { onAdded: () => void }) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("readonly");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/users", {
        body: { username, password, role },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      setUsername("");
      setPassword("");
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-wrap items-end gap-2 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Username
        <input
          className={inputStyle}
          value={username}
          autoComplete="off"
          onChange={(event) => setUsername(event.target.value)}
        />
      </label>
      <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
        Password
        <input
          className={inputStyle}
          type="password"
          value={password}
          autoComplete="new-password"
          onChange={(event) => setPassword(event.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-ink-muted">
        Role
        <select
          className={inputStyle}
          value={role}
          onChange={(event) => setRole(event.target.value as Role)}
        >
          {ROLES.map((entry) => (
            <option key={entry.id} value={entry.id}>
              {entry.label}
            </option>
          ))}
        </select>
      </label>
      <button
        type="submit"
        disabled={!username.trim() || password === "" || create.isPending}
        className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                   focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        {create.isPending ? "Adding…" : "Add user"}
      </button>
      {create.isError && (
        <p className="w-full text-xs text-danger">
          That account could not be created. The username may already be taken.
        </p>
      )}
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
