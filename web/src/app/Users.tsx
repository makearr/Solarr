import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, ShieldAlert, ShieldCheck, Trash2 } from "lucide-react";

import { api } from "@/api/client";
import { useAuthPolicy, useSetAuthRequired } from "@/lib/useAuthPolicy";
import { useSetMyTier } from "@/lib/useTier";
import type { components } from "@/api/schema";

type UserAccount = components["schemas"]["UserAccount"];
type Role = components["schemas"]["Role"];
type Tier = components["schemas"]["Me"]["tier"];
type APIKey = components["schemas"]["APIKey"];
type Permission = components["schemas"]["Permission"];
type UserLimits = components["schemas"]["UserLimits"];
type CategoryGrant = components["schemas"]["CategoryGrant"];

const TIER_RANK: Record<Tier, number> = { normal: 0, advanced: 1, expert: 2 };

const PERMISSIONS: readonly { id: Permission; label: string }[] = [
  { id: "none", label: "No access" },
  { id: "read", label: "Read" },
  { id: "request", label: "Request" },
  { id: "manage", label: "Manage" },
];

const TIERS: readonly { id: Tier; label: string; blurb: string }[] = [
  { id: "normal", label: "Normal", blurb: "Browsing, wanting and adding. Everything a working library needs." },
  { id: "advanced", label: "Advanced", blurb: "Adds Import, Activity, Unidentified, Packs, Naming and new libraries." },
  { id: "expert", label: "Expert", blurb: "Adds quality and language profiles, cache sizing and internal knobs." },
];

/** The User settings page: who can sign in (Access), the accounts
 *  themselves, and the caller's own tier. Split out of one long Settings
 *  scroll 2026-09-11. */
export function UserSettings() {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me");
      if (error) throw error;
      return data;
    },
  });

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto flex max-w-5xl flex-col gap-6 p-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-base font-semibold">User</h1>
        </header>

        {me.data && <MyTierSection tier={me.data.tier} tierMax={me.data.tier_max} />}
        <APIKeysSection />
        {/* Only an administrator may change it; the server refuses anyone else. */}
        {me.data?.role === "admin" && <AccessSection />}

        {me.data && <Users currentUserId={me.data.user_id} />}
      </div>
    </div>
  );
}

/** ADR-26's own self-service switch: any authenticated user, not just an
 *  administrator, may change how much of the settings surface they see.
 *  Raising or lowering it never changes behaviour, only what is displayed. */
function MyTierSection({ tier, tierMax }: { tier: Tier; tierMax: Tier }) {
  const setTier = useSetMyTier();

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold text-ink">
        Interface level
      </h2>
      <div className="flex flex-col gap-2 rounded-lg border border-border-subtle bg-surface p-3">
        <p className="text-xs text-ink-muted">
          How much of the interface you see -- each level includes the ones below it. Purely a
          display preference: it never changes what Solarr does.
        </p>
        <div className="flex flex-wrap gap-2">
          {TIERS.map((option) => (
            <button
              key={option.id}
              type="button"
              title={option.blurb}
              disabled={setTier.isPending || TIER_RANK[option.id] > TIER_RANK[tierMax]}
              aria-pressed={tier === option.id}
              onClick={() => setTier.mutate(option.id)}
              className={`rounded-md border px-2.5 py-1 text-xs disabled:opacity-50 ${
                tier === option.id
                  ? "border-accent bg-accent/10 text-accent"
                  : "border-border-subtle text-ink-muted hover:text-ink"
              }`}
            >
              {option.label}
            </button>
          ))}
        </div>
        {tierMax !== "expert" && (
          <p className="text-xs text-ink-muted">
            An administrator allows this account up to{" "}
            {TIERS.find((option) => option.id === tierMax)?.label}.
          </p>
        )}
        {setTier.isError && <p className="text-xs text-danger">That could not be saved.</p>}
      </div>
    </section>
  );
}

/** Self-service, non-interactive credentials for an upstream app (Prowlarr
 *  and similar) that authenticates as this user rather than a person signing
 *  in -- the mechanism (X-Api-Key/Bearer, settings.Cache.CreateAPIKey) already
 *  existed and was checked on every request; this is the first screen that
 *  can actually reach it. */
function APIKeysSection() {
  const queryClient = useQueryClient();
  const keys = useQuery({
    queryKey: ["me", "apikeys"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me/apikeys");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["me", "apikeys"] });

  const [justCreated, setJustCreated] = useState<{ name: string; key: string } | null>(null);
  const [name, setName] = useState("");

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/me/apikeys", { body: { name } });
      if (error) throw error;
      return data;
    },
    onSuccess: (created) => {
      if (created) setJustCreated({ name: created.name, key: created.key });
      setName("");
      invalidate();
    },
  });

  const revoke = useMutation({
    mutationFn: async (id: number) => {
      const { error } = await api.DELETE("/me/apikeys/{id}", { params: { path: { id } } });
      if (error) throw error;
    },
    onSuccess: invalidate,
  });

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold text-ink">API keys</h2>
      <div className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3">
        <p className="text-xs text-ink-muted">
          For an upstream app that authenticates as you (an *Arr-style X-Api-Key or Bearer
          header) rather than a person signing in. Shown once, at creation -- store it there.
        </p>

        {justCreated && (
          <div className="flex flex-col gap-1.5 rounded-md border border-accent bg-accent/10 p-2.5">
            <span className="text-xs text-ink-muted">
              "{justCreated.name}" -- copy this now, it will not be shown again:
            </span>
            <div className="flex items-center gap-1.5">
              <code className="min-w-0 flex-1 truncate rounded bg-canvas px-2 py-1 font-mono text-xs">
                {justCreated.key}
              </code>
              <button
                type="button"
                aria-label="Copy key"
                onClick={() => {
                  navigator.clipboard?.writeText(justCreated.key).catch(() => {});
                }}
                className="shrink-0 rounded-md border border-border-subtle p-1.5 text-ink-muted
                           hover:text-ink"
              >
                <Copy aria-hidden className="size-3.5" />
              </button>
              <button
                type="button"
                onClick={() => setJustCreated(null)}
                className="shrink-0 rounded-md border border-border-subtle px-2 py-1.5 text-xs
                           text-ink-muted hover:text-ink"
              >
                Done
              </button>
            </div>
          </div>
        )}

        {(keys.data ?? []).length > 0 && (
          <ul className="flex flex-col divide-y divide-border-subtle">
            {(keys.data ?? []).map((key) => (
              <APIKeyRow
                key={key.id}
                apiKey={key}
                busy={revoke.isPending}
                onRevoke={() => revoke.mutate(key.id)}
              />
            ))}
          </ul>
        )}

        {(create.isError || revoke.isError) && (
          <p className="text-xs text-danger">That could not be saved.</p>
        )}

        <form
          className="flex items-end gap-2"
          onSubmit={(event) => {
            event.preventDefault();
            if (name.trim() !== "") create.mutate();
          }}
        >
          <label className="flex min-w-32 flex-1 flex-col gap-1 text-xs text-ink-muted">
            Name
            <input
              className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm
                         text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                         focus-visible:outline-accent"
              value={name}
              placeholder="Prowlarr"
              autoComplete="off"
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <button
            type="submit"
            disabled={name.trim() === "" || create.isPending}
            className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                       focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            {create.isPending ? "Creating…" : "New key"}
          </button>
        </form>
      </div>
    </section>
  );
}

function APIKeyRow({
  apiKey,
  busy,
  onRevoke,
}: {
  apiKey: APIKey;
  busy: boolean;
  onRevoke: () => void;
}) {
  return (
    <li className="flex items-center gap-2 py-2 text-sm">
      <div className="flex min-w-0 flex-1 flex-col">
        <span className="truncate font-medium">{apiKey.name}</span>
        <span className="text-xs text-ink-muted">
          Created {new Date(apiKey.created_at).toLocaleDateString()}
          {apiKey.last_used_at
            ? ` · last used ${new Date(apiKey.last_used_at).toLocaleDateString()}`
            : " · never used"}
        </span>
      </div>
      <button
        type="button"
        aria-label={`Revoke ${apiKey.name}`}
        onClick={() => {
          if (window.confirm(`Revoke "${apiKey.name}"? Anything using it will stop working immediately.`)) {
            onRevoke();
          }
        }}
        disabled={busy}
        className="shrink-0 rounded-md border border-border-subtle p-1 text-ink-muted
                   hover:text-danger disabled:opacity-50 focus-visible:outline-2
                   focus-visible:outline-offset-2 focus-visible:outline-accent"
      >
        <Trash2 aria-hidden className="size-3.5" />
      </button>
    </li>
  );
}

/** The login requirement. Kept at the top of User settings because it is the
 *  one setting that changes who everything else is answered as. */
function AccessSection() {
  const policy = useAuthPolicy();
  const change = useSetAuthRequired();
  const [password, setPassword] = useState("");

  if (!policy.data) return null;
  const required = policy.data.auth_required;
  // Turning a login back on has to set a password in the same step when no
  // account has one, or the interface locks itself out.
  const needsPassword = !required && !policy.data.can_sign_in;

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold text-ink">Access</h2>
      <div
        className={`flex flex-col gap-3 rounded-lg border bg-surface p-3 ${
          required ? "border-border-subtle" : "border-warn"
        }`}
      >
        <div className="flex items-start gap-2.5">
          {required ? (
            <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0 text-ok" />
          ) : (
            <ShieldAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warn" />
          )}
          <div className="flex flex-col gap-1">
            <span className="text-sm font-medium">
              {required ? "A login is required" : "No login is required"}
            </span>
            <span className="text-xs text-ink-muted">
              {required
                ? `Callers authenticate by ${policy.data.configured_mode}, set in config.yml.`
                : "Every request is served as the administrator. Only appropriate where " +
                  "something else already controls who can reach the port."}
            </span>
          </div>
        </div>

        {needsPassword && (
          <label className="flex flex-col gap-1 text-xs text-ink-muted">
            Password for{" "}
            <span className="font-mono text-ink">{policy.data.administrator ?? "the administrator"}</span>
            <input
              type="password"
              value={password}
              autoComplete="new-password"
              onChange={(event) => setPassword(event.target.value)}
              className="rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm
                         text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                         focus-visible:outline-accent"
            />
          </label>
        )}

        {change.isError && (
          <p className="text-xs text-danger">That could not be saved.</p>
        )}

        <button
          type="button"
          onClick={() =>
            change.mutate({
              authRequired: !required,
              password: needsPassword ? password : undefined,
            })
          }
          disabled={change.isPending || (needsPassword && password === "")}
          className="self-start rounded-md border border-border-subtle px-2.5 py-1 text-xs
                     text-ink-muted hover:text-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          {change.isPending
            ? "Saving…"
            : required
              ? "Stop requiring a login"
              : "Require a login"}
        </button>
      </div>
    </section>
  );
}

const ROLES: readonly { id: Role; label: string; blurb: string }[] = [
  { id: "admin", label: "Administrator", blurb: "Changes settings, indexers and downloaders." },
  { id: "operator", label: "Operator", blurb: "Works the queue; libraries are granted per category." },
  { id: "readonly", label: "Read only", blurb: "Looks, and requests where allowed." },
];

/** Accounts and what they may do: role, the libraries each may use, and the interface levels an
 *  administrator allows. Only administrators reach any of it. */
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
  const categories = useQuery({
    queryKey: ["categories"],
    queryFn: async () => {
      const { data, error } = await api.GET("/categories");
      if (error) throw error;
      return data;
    },
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ["users"] });

  const update = useMutation({
    mutationFn: async (change: { id: number; role?: Role; password?: string; limits?: UserLimits }) => {
      const { data, error } = await api.PATCH("/users/{id}", {
        params: { path: { id: change.id } },
        body: { role: change.role, password: change.password, limits: change.limits },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: invalidate,
  });

  const grant = useMutation({
    mutationFn: async (change: { id: number; categoryId: number; permission: Permission }) => {
      const { error } = await api.PUT("/users/{id}/access", {
        params: { path: { id: change.id } },
        body: { category_id: change.categoryId, permission: change.permission },
      });
      if (error) throw error;
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
  const libraries = categories.data ?? [];

  return (
    <section className="flex flex-col gap-2">
      <h2 className="text-sm font-semibold text-ink">Users</h2>

      <ul className="divide-y divide-border-subtle rounded-lg border border-border-subtle bg-surface">
        {(users.data ?? []).map((user) => (
          <Row
            key={user.id}
            user={user}
            libraries={libraries}
            isSelf={user.id === currentUserId}
            onRole={(role) => update.mutate({ id: user.id, role })}
            onPassword={(password) => update.mutate({ id: user.id, password })}
            onLimits={(limits) => update.mutate({ id: user.id, limits })}
            onGrant={(categoryId, permission) => grant.mutate({ id: user.id, categoryId, permission })}
            onRemove={() => remove.mutate(user.id)}
            busy={update.isPending || remove.isPending || grant.isPending}
          />
        ))}
      </ul>

      {(update.isError || remove.isError || grant.isError) && (
        <p className="text-xs text-danger">
          That change was refused. The last administrator cannot be removed or demoted, and an
          account cannot start above its maximum level.
        </p>
      )}

      <AddUser libraries={libraries} onAdded={invalidate} />
      <NewLibraryDefault />
    </section>
  );
}

type Library = { id: number; name: string; media_type: string };

/** What a new account is offered for libraries created after it. */
function NewLibraryDefault() {
  const queryClient = useQueryClient();
  const defaults = useQuery({
    queryKey: ["user-defaults"],
    queryFn: async () => {
      const { data, error } = await api.GET("/settings/users");
      if (error) throw error;
      return data;
    },
  });
  const save = useMutation({
    mutationFn: async (permission: Permission) => {
      const { error } = await api.PUT("/settings/users", { body: { new_library_permission: permission } });
      if (error) throw error;
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["user-defaults"] }),
  });
  if (!defaults.data) return null;

  return (
    <label className="flex flex-wrap items-center gap-2 rounded-lg border border-border-subtle bg-surface p-3 text-xs text-ink-muted">
      <span className="flex-1">
        New accounts get this on libraries created later. Each account can be changed on its own.
      </span>
      <PermissionSelect
        label="Default for new libraries"
        value={defaults.data.new_library_permission}
        disabled={save.isPending}
        onChange={(permission) => save.mutate(permission)}
      />
    </label>
  );
}

function PermissionSelect({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string;
  value: Permission;
  disabled?: boolean;
  onChange: (permission: Permission) => void;
}) {
  return (
    <select
      aria-label={label}
      value={value}
      disabled={disabled}
      onChange={(event) => onChange(event.target.value as Permission)}
      className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs disabled:opacity-60"
    >
      {PERMISSIONS.map((permission) => (
        <option key={permission.id} value={permission.id}>
          {permission.label}
        </option>
      ))}
    </select>
  );
}

function TierSelect({
  label,
  value,
  max,
  disabled,
  onChange,
}: {
  label: string;
  value: Tier;
  max?: Tier;
  disabled?: boolean;
  onChange: (tier: Tier) => void;
}) {
  return (
    <select
      aria-label={label}
      value={value}
      disabled={disabled}
      onChange={(event) => onChange(event.target.value as Tier)}
      className="rounded-md border border-border-subtle bg-canvas px-1.5 py-0.5 text-xs"
    >
      {TIERS.filter((tier) => max === undefined || TIER_RANK[tier.id] <= TIER_RANK[max]).map((tier) => (
        <option key={tier.id} value={tier.id}>
          {tier.label}
        </option>
      ))}
    </select>
  );
}

/** Starts at, at most, and what later libraries grant: the three things an administrator decides. */
function LimitsFields({
  who,
  limits,
  isAdmin,
  disabled,
  onChange,
}: {
  who: string;
  limits: UserLimits;
  isAdmin: boolean;
  disabled?: boolean;
  onChange: (limits: UserLimits) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-ink-muted">
      <span className="flex items-center gap-1.5">
        Starts at
        <TierSelect
          label={`Starting interface level for ${who}`}
          value={limits.tier_default}
          max={limits.tier_max}
          disabled={disabled}
          onChange={(tier) => onChange({ ...limits, tier_default: tier })}
        />
      </span>
      <span className="flex items-center gap-1.5">
        at most
        <TierSelect
          label={`Highest interface level for ${who}`}
          value={limits.tier_max}
          disabled={disabled}
          onChange={(tier) =>
            onChange({
              ...limits,
              tier_max: tier,
              tier_default: TIER_RANK[limits.tier_default] > TIER_RANK[tier] ? tier : limits.tier_default,
            })
          }
        />
      </span>
      <span className="flex items-center gap-1.5">
        New libraries
        {isAdmin ? (
          <span className="text-ink">managed</span>
        ) : (
          <PermissionSelect
            label={`Access for ${who} to new libraries`}
            value={limits.new_library_permission}
            disabled={disabled}
            onChange={(permission) => onChange({ ...limits, new_library_permission: permission })}
          />
        )}
      </span>
    </div>
  );
}

/** Every library and what this account may do in it; an administrator manages them all. */
function AccessGrid({
  who,
  libraries,
  grants,
  isAdmin,
  disabled,
  onChange,
}: {
  who: string;
  libraries: readonly Library[];
  grants: readonly CategoryGrant[];
  isAdmin: boolean;
  disabled?: boolean;
  onChange: (categoryId: number, permission: Permission) => void;
}) {
  if (libraries.length === 0) {
    return <p className="text-xs text-ink-muted">No libraries yet.</p>;
  }
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 text-xs">
      {libraries.map((library) => {
        const permission = grants.find((entry) => entry.category_id === library.id)?.permission ?? "none";
        return (
          <label key={library.id} className="contents">
            <span className={permission === "none" && !isAdmin ? "text-ink-muted" : "text-ink"}>
              {library.name}
            </span>
            <PermissionSelect
              label={`${who}: ${library.name}`}
              value={isAdmin ? "manage" : permission}
              disabled={disabled || isAdmin}
              onChange={(next) => onChange(library.id, next)}
            />
          </label>
        );
      })}
    </div>
  );
}

function Row({
  user,
  libraries,
  isSelf,
  onRole,
  onPassword,
  onLimits,
  onGrant,
  onRemove,
  busy,
}: {
  user: UserAccount;
  libraries: readonly Library[];
  isSelf: boolean;
  onRole: (role: Role) => void;
  onPassword: (password: string) => void;
  onLimits: (limits: UserLimits) => void;
  onGrant: (categoryId: number, permission: Permission) => void;
  onRemove: () => void;
  busy: boolean;
}) {
  const [password, setPassword] = useState("");
  const [resetting, setResetting] = useState(false);
  const [open, setOpen] = useState(false);
  const isAdmin = user.role === "admin";
  const granted = libraries.filter((library) =>
    user.access.some((entry) => entry.category_id === library.id),
  );

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
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="rounded-md border border-border-subtle px-2 py-0.5 text-xs text-ink-muted
                     hover:text-ink focus-visible:outline-2 focus-visible:outline-offset-2
                     focus-visible:outline-accent"
        >
          Access
        </button>

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

      <p className="text-xs text-ink-muted">
        {isAdmin
          ? "Manages every library."
          : granted.length === 0
            ? "No libraries: this account sees nothing yet."
            : granted
                .map((library) => {
                  const permission = user.access.find((entry) => entry.category_id === library.id)?.permission;
                  return `${library.name} (${PERMISSIONS.find((entry) => entry.id === permission)?.label.toLowerCase()})`;
                })
                .join(" · ")}
        {" · "}
        {TIERS.find((tier) => tier.id === user.tier)?.label} interface
        {user.limits.tier_max !== "expert" &&
          `, at most ${TIERS.find((tier) => tier.id === user.limits.tier_max)?.label}`}
      </p>

      {open && (
        <div className="flex flex-col gap-3 rounded-md border border-border-subtle bg-canvas p-2.5">
          <AccessGrid
            who={user.username}
            libraries={libraries}
            grants={user.access}
            isAdmin={isAdmin}
            disabled={busy}
            onChange={onGrant}
          />
          <LimitsFields
            who={user.username}
            limits={user.limits}
            isAdmin={isAdmin}
            disabled={busy}
            onChange={onLimits}
          />
        </div>
      )}

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

function AddUser({ libraries, onAdded }: { libraries: readonly Library[]; onAdded: () => void }) {
  const defaults = useQuery({
    queryKey: ["user-defaults"],
    queryFn: async () => {
      const { data, error } = await api.GET("/settings/users");
      if (error) throw error;
      return data;
    },
  });
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<Role>("readonly");
  const [grants, setGrants] = useState<CategoryGrant[]>([]);
  const [limits, setLimits] = useState<UserLimits | null>(null);
  const shownLimits: UserLimits = limits ?? {
    tier_default: "normal",
    tier_max: "expert",
    new_library_permission: defaults.data?.new_library_permission ?? "none",
  };

  const create = useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST("/users", {
        body: { username, password, role, access: grants, limits: shownLimits },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => {
      setUsername("");
      setPassword("");
      setGrants([]);
      setLimits(null);
      onAdded();
    },
  });

  return (
    <form
      className="flex flex-col gap-3 rounded-lg border border-border-subtle bg-surface p-3"
      onSubmit={(event) => {
        event.preventDefault();
        create.mutate();
      }}
    >
      <div className="flex flex-wrap items-end gap-2">
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
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="text-xs text-ink-muted">Libraries</span>
        <AccessGrid
          who="the new account"
          libraries={libraries}
          grants={grants}
          isAdmin={role === "admin"}
          onChange={(categoryId, permission) =>
            setGrants([
              ...grants.filter((entry) => entry.category_id !== categoryId),
              ...(permission === "none" ? [] : [{ category_id: categoryId, permission }]),
            ])
          }
        />
      </div>

      <LimitsFields
        who="the new account"
        limits={shownLimits}
        isAdmin={role === "admin"}
        onChange={setLimits}
      />

      <div className="flex flex-wrap items-center gap-2">
        <button
          type="submit"
          disabled={!username.trim() || password === "" || create.isPending}
          className="rounded-md bg-accent px-3 py-1.5 text-sm text-accent-ink disabled:opacity-50
                     focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
        >
          {create.isPending ? "Adding…" : "Add user"}
        </button>
        {create.isError && (
          <p className="text-xs text-danger">
            That account could not be created. The username may already be taken.
          </p>
        )}
      </div>
    </form>
  );
}

const inputStyle =
  "rounded-md border border-border-subtle bg-canvas px-2 py-1.5 text-sm text-ink " +
  "focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent";
