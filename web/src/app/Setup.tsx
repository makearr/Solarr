import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";
import { MEDIA_TYPES } from "./Categories";

type ArrApp = components["schemas"]["ArrApp"];
type MediaType = components["schemas"]["MediaType"];

const ARR_APPS: readonly ArrApp[] = ["lidarr", "sonarr", "radarr", "readarr", "whisparr"];

// A convenience, not a gate (section 21.11): every step can be skipped.
export function Setup({ onDone }: { onDone: () => void }) {
  // Local to this mount, same as App.tsx's own "skipped" -- there is no
  // backend concept of "the *Arr step is done", since connecting is
  // optional and idempotent (POST /arr/instances again just adds a second
  // connection). Asked for 2026-09-11: between the account and the first
  // root folder, since the connection's own root folder paths are what a
  // real user picks their first library folder from.
  const [arrStepDone, setArrStepDone] = useState(false);

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
          <h1 className="text-lg font-semibold tracking-tight">Set up Solarr</h1>
          <p className="text-sm text-ink-muted">
            Three steps. You can skip any of them and change it later.
          </p>
        </header>

        {status.data.needs_administrator ? (
          <CreateAdministrator />
        ) : !arrStepDone ? (
          <ConnectArr
            onSkip={() => setArrStepDone(true)}
            onConnected={(rootFoldersImported) => (rootFoldersImported ? onDone() : setArrStepDone(true))}
          />
        ) : (
          <AddRootFolder onSkip={onDone} onAdded={onDone} />
        )}
      </div>
    </Centered>
  );
}

// What a fresh connection to each app suggests for its own library -- a
// starting point, always editable, not a rule (nothing stops naming a
// Lidarr-backed library "Vinyl" instead).
const LIBRARY_SUGGESTION: Record<ArrApp, { name: string; mediaType: MediaType }> = {
  lidarr: { name: "Music", mediaType: "music" },
  sonarr: { name: "TV", mediaType: "tv" },
  radarr: { name: "Movies", mediaType: "movie" },
  readarr: { name: "Books", mediaType: "book" },
  whisparr: { name: "Adult", mediaType: "adult" },
};

interface ConnectOutcome {
  rootFoldersImported: boolean;
  rootFolderErrors: string[];
}

function ConnectArr({
  onSkip,
  onConnected,
}: {
  onSkip: () => void;
  onConnected: (rootFoldersImported: boolean) => void;
}) {
  const [app, setApp] = useState<ArrApp>("lidarr");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [libraryName, setLibraryName] = useState(LIBRARY_SUGGESTION.lidarr.name);
  const [libraryNameTouched, setLibraryNameTouched] = useState(false);
  const [mediaType, setMediaType] = useState<MediaType>(LIBRARY_SUGGESTION.lidarr.mediaType);
  const [mediaTypeTouched, setMediaTypeTouched] = useState(false);
  const [importRootFolders, setImportRootFolders] = useState(true);
  const [importContent, setImportContent] = useState(false);
  const [outcome, setOutcome] = useState<ConnectOutcome | null>(null);

  const changeApp = (value: ArrApp) => {
    setApp(value);
    const suggestion = LIBRARY_SUGGESTION[value];
    if (!libraryNameTouched) setLibraryName(suggestion.name);
    if (!mediaTypeTouched) setMediaType(suggestion.mediaType);
  };

  const connect = useMutation({
    mutationFn: async (): Promise<ConnectOutcome> => {
      const { data: category, error: categoryError } = await api.POST("/categories", {
        body: { name: libraryName, media_type: mediaType },
      });
      if (categoryError) throw categoryError;

      const { data: instance, error: instanceError } = await api.POST("/arr/instances", {
        body: { app, name, base_url: baseUrl, api_key: apiKey, category_id: category!.id, enabled: true },
      });
      if (instanceError) throw instanceError;

      let rootFoldersImported = false;
      const rootFolderErrors: string[] = [];
      if (importRootFolders) {
        const { data: remoteRoots, error: rootsError } = await api.GET("/arr/instances/{id}/rootfolders", {
          params: { path: { id: instance!.id } },
        });
        if (rootsError) {
          rootFolderErrors.push(
            typeof rootsError === "object" && rootsError !== null && "detail" in rootsError
              ? String((rootsError as { detail: unknown }).detail)
              : "The instance's own root folders could not be read.",
          );
        } else {
          for (const remote of remoteRoots ?? []) {
            const { data: root, error: rootError } = await api.POST("/rootfolders", {
              body: { path: remote.path },
            });
            if (rootError || !root) {
              rootFolderErrors.push(`${remote.path}: could not be used here -- check it is mounted.`);
              continue;
            }
            const { error: attachError } = await api.POST("/categories/{id}/rootfolders", {
              params: { path: { id: category!.id } },
              body: { root_folder_id: root.id },
            });
            if (attachError) {
              rootFolderErrors.push(`${remote.path}: added, but could not be attached to ${libraryName}.`);
              continue;
            }
            rootFoldersImported = true;
          }
        }
      }

      if (importContent) {
        const { error: syncError } = await api.POST("/arr/instances/{id}/sync", {
          params: { path: { id: instance!.id } },
        });
        if (syncError) rootFolderErrors.push("Connected, but the import could not be started.");
      }

      return { rootFoldersImported, rootFolderErrors };
    },
    onSuccess: (result) => {
      setOutcome(result);
      if (result.rootFolderErrors.length === 0) onConnected(result.rootFoldersImported);
    },
  });

  return (
    <Panel
      title="Step 2 — Import from an *Arr application"
      description={
        "Already run Lidarr (or one of the others)? Connect it now and what it monitors " +
        "comes in as intent -- artists become monitored people, albums become wanted items. " +
        "Only Lidarr syncs for real yet; the rest can still be connected for when their own " +
        "stage ships."
      }
    >
      <form
        className="space-y-3"
        onSubmit={(event) => {
          event.preventDefault();
          setOutcome(null);
          connect.mutate();
        }}
      >
        <Field label="Application">
          <select
            className={inputStyle}
            value={app}
            onChange={(event) => changeApp(event.target.value as ArrApp)}
          >
            {ARR_APPS.map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Name" hint="Shown in the interface, e.g. Home Lidarr.">
          <input
            className={inputStyle}
            value={name}
            placeholder="Home Lidarr"
            autoComplete="off"
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label="Base URL">
          <input
            className={inputStyle}
            value={baseUrl}
            placeholder="http://lidarr:8686"
            autoComplete="off"
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </Field>
        <Field label="API key">
          <input
            className={inputStyle}
            type="password"
            value={apiKey}
            autoComplete="off"
            onChange={(event) => setApiKey(event.target.value)}
          />
        </Field>

        <div className="flex gap-2">
          <Field label="Library name">
            <input
              className={inputStyle}
              value={libraryName}
              onChange={(event) => {
                setLibraryNameTouched(true);
                setLibraryName(event.target.value);
              }}
            />
          </Field>
          <Field label="Media type">
            <select
              className={inputStyle}
              value={mediaType}
              onChange={(event) => {
                setMediaTypeTouched(true);
                setMediaType(event.target.value as MediaType);
              }}
            >
              {MEDIA_TYPES.map((value) => (
                <option key={value} value={value}>
                  {value}
                </option>
              ))}
            </select>
          </Field>
        </div>

        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={importRootFolders}
            onChange={(event) => setImportRootFolders(event.target.checked)}
          />
          Import the root folder(s) from {app}
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={importContent}
            onChange={(event) => setImportContent(event.target.checked)}
          />
          Import content now (artists and albums; can take a while for a large library)
        </label>

        {connect.isError && <Problem error={connect.error} />}
        {outcome && outcome.rootFolderErrors.length > 0 && (
          <div className="space-y-1 rounded-md border border-warn/40 bg-warn/10 p-2 text-xs text-warn">
            {outcome.rootFolderErrors.map((message, index) => (
              <p key={index}>{message}</p>
            ))}
            <p>Connected regardless -- fix the rest from Settings, or add the folder manually next.</p>
          </div>
        )}

        <div className="flex gap-2">
          <button
            type="submit"
            disabled={
              !name.trim() || !baseUrl.trim() || !apiKey.trim() || !libraryName.trim() || connect.isPending
            }
            className={primaryButtonStyle}
          >
            {connect.isPending ? "Connecting…" : "Connect"}
          </button>
          <button type="button" onClick={onSkip} className={quietButtonStyle}>
            Skip for now
          </button>
          {outcome && outcome.rootFolderErrors.length > 0 && (
            <button
              type="button"
              onClick={() => onConnected(outcome.rootFoldersImported)}
              className={quietButtonStyle}
            >
              Continue anyway
            </button>
          )}
        </div>
      </form>
    </Panel>
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
          disabled={!username.trim() || password === "" || create.isPending}
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
      title="Step 3 — Add a library folder"
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
