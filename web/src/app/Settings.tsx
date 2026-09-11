import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ShieldAlert, ShieldCheck } from "lucide-react";

import { api } from "@/api/client";
import { useAuthPolicy, useSetAuthRequired } from "@/lib/useAuthPolicy";
import { CacheSettings } from "./CacheSettings";
import { Categories } from "./Categories";
import { Network } from "./Network";
import { Scheduler } from "./Scheduler";
import { UnwantedFiles } from "./UnwantedFiles";
import { Users } from "./Users";

/** Settings sections in the order they matter to someone setting the thing up.
 *  `stage` names what has to exist before a section can do anything, so the
 *  scaffolding is honest about being scaffolding rather than looking broken.
 *  Storage moved out to its own sidebar section (todo 4.18) -- configuration
 *  lives where the thing it configures lives, and root folders are no
 *  longer just-a-list here. */
const SECTIONS = [
  { id: "profiles", title: "Quality profiles", blurb: "Hard gates, tier order and scoring.", stage: 4 },
  { id: "indexers", title: "Indexers", blurb: "Newznab and Torznab sources, and their egress.", stage: 5 },
  { id: "downloaders", title: "Download clients", blurb: "Usenet, torrent and direct downloads.", stage: 5 },
  { id: "metadata", title: "Metadata", blurb: "Providers, field priority and refresh cadence.", stage: 3 },
  { id: "general", title: "General", blurb: "Interface, backups and maintenance." },
] as const;

export function Settings() {
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
          <h1 className="text-base font-semibold">Settings</h1>
          <p className="text-xs text-ink-muted">
            Every setting is normal, advanced or expert (ADR-26). The tier selector arrives with
            the forms themselves.
          </p>
        </header>

        <AccessSection />

        {me.data && <Users currentUserId={me.data.user_id} />}

        <Categories />

        <UnwantedFiles />

        <Network />

        <Scheduler />

        <CacheSettings />

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Import</h2>
          <Link
            to="/settings/arr"
            className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface
                       px-3 py-2.5 hover:border-accent focus-visible:outline-2
                       focus-visible:outline-offset-2 focus-visible:outline-accent"
          >
            <span className="text-sm font-medium">*Arr import</span>
            <span className="text-xs text-ink-muted">
              Bring in what Lidarr (and, once their own stage ships, Sonarr/Radarr/Readarr/
              Whisparr) already monitors, as intent -- todo 4.13.
            </span>
          </Link>
        </section>

        <section className="flex flex-col gap-2">
          <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Sections</h2>
          <ul className="grid gap-3 sm:grid-cols-2">
            {SECTIONS.map((section) => (
              <li
                key={section.id}
                className="flex flex-col gap-1 rounded-lg border border-border-subtle bg-surface
                           px-3 py-2.5"
              >
                <span className="flex items-baseline gap-2">
                  <span className="text-sm font-medium">{section.title}</span>
                  {"stage" in section && (
                    <span className="ml-auto shrink-0 rounded-full border border-border-subtle
                                     px-1.5 py-px text-[0.6875rem] text-ink-muted">
                      stage {section.stage}
                    </span>
                  )}
                </span>
                <span className="text-xs text-ink-muted">{section.blurb}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>
    </div>
  );
}

/** The login requirement. Kept at the top of settings because it is the one
 *  setting that changes who everything else is answered as. */
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
      <h2 className="text-xs font-medium uppercase tracking-wide text-ink-muted">Access</h2>
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
