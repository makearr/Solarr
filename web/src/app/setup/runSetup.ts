import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";
import { t } from "@/lib/i18n";

import { detailOf } from "./ui";
import { libraryOf, replaceConnection, replaceLibrary, type WizardConnection, type WizardLibrary, type WizardState } from "./wizardState";
import { starterProfile } from "./QualityStep";

type SyncOptions = components["schemas"]["ArrSyncOptions"];

/** One piece of work the assistant does at Finish; it can be run again, because each looks for what is already there first. */
export interface Task {
  id: string;
  label: string;
  /** Tasks that must have worked before this one makes sense. */
  needs: string[];
  run: (hooks: TaskHooks) => Promise<void>;
}

export interface TaskHooks {
  state: () => WizardState;
  commit: (next: WizardState) => void;
}

/** The folders a library holds: what was typed on the fresh path, the places its *Arr's folders are here on the migrate path. */
export function foldersOf(state: WizardState, library: WizardLibrary): string[] {
  const connection = state.connections.find((one) => one.libraryKey === library.key);
  const folders = connection ? connection.folderMaps.map((map) => map.here) : library.folders;
  return [...new Set(folders.map((folder) => folder.trim().replace(/(.)\/+$/, "$1")).filter((folder) => folder.startsWith("/")))];
}

export function libraryIdOf(library: WizardLibrary | undefined): number | undefined {
  return library?.libraryId ?? library?.madeId;
}

/** The options an import is previewed and run with, from what was chosen for one connection. */
export function syncOptions(connection: WizardConnection, libraryId: number | undefined): SyncOptions {
  const { choices } = connection;
  return {
    mode: "add",
    monitored: choices.monitored,
    unmonitored: choices.unmonitored,
    albums: "all",
    limit: 0,
    indexers: choices.indexers,
    download_clients: choices.downloadClients,
    import_lists: choices.importLists,
    path_rewrites: connection.folderMaps.filter((map) => map.here.trim() && map.here !== map.arrPath).map((map) => ({ from: map.arrPath, to: map.here.trim() })),
    new_folders_library_id: libraryId,
  };
}

/** Everything Finish does, in the order it must happen. */
export function buildTasks(state: WizardState): Task[] {
  const tasks: Task[] = [];
  if (state.languages && state.languages.length > 0) {
    tasks.push({
      id: "languages", label: t("setup.task.languages"), needs: [],
      run: async ({ state: current }) => {
        await ok(api.PUT("/defaultlanguages", { body: { languages: current().languages ?? [] } }));
      },
    });
  }
  if (state.connections.some((connection) => connection.app !== "prowlarr") && state.companion !== false) {
    tasks.push({
      id: "companion", label: t("setup.task.companion"), needs: [],
      run: async () => {
        await ok(api.PATCH("/settings/general", { body: { solarr_mode: "arr_companion" } }));
      },
    });
  }
  for (const library of state.libraries) {
    tasks.push({ id: `library:${library.key}`, label: t("setup.task.library", { name: library.name }), needs: ["languages"], run: (hooks) => makeLibrary(hooks, library.key) });
    if (["best", "balanced", "space"].includes(library.starter) && library.libraryId === undefined) {
      tasks.push({ id: `quality:${library.key}`, label: t("setup.task.quality", { name: library.name }), needs: [`library:${library.key}`], run: (hooks) => setQuality(hooks, library.key) });
    }
  }
  for (const connection of state.connections.filter((one) => one.app === "prowlarr")) {
    tasks.push({
      id: `prowlarr:${connection.instanceId}`, label: t("setup.task.prowlarr", { name: connection.name }), needs: [],
      run: async () => {
        await ok(api.POST("/arr/instances/{id}/prowlarr-sync", { params: { path: { id: connection.instanceId } } }));
      },
    });
  }
  const prowlarrTasks = state.connections.filter((one) => one.app === "prowlarr").map((one) => `prowlarr:${one.instanceId}`);
  for (const connection of state.connections.filter((one) => one.libraryKey !== undefined)) {
    const libraryTask = `library:${connection.libraryKey}`;
    tasks.push({ id: `link:${connection.instanceId}`, label: t("setup.task.link", { name: connection.name }), needs: [libraryTask], run: (hooks) => linkConnection(hooks, connection.instanceId) });
    tasks.push({
      id: `import:${connection.instanceId}`, label: t("setup.task.import", { name: connection.name }),
      needs: [`link:${connection.instanceId}`, ...prowlarrTasks],
      run: (hooks) => startImport(hooks, connection.instanceId),
    });
  }
  const features = state.features ?? [];
  if (features.length > 0) {
    tasks.push({
      id: "features", label: t("setup.task.features", { names: features.map((feature) => t(`setup.feature.${feature}`).toLowerCase()).join(", ") }), needs: [],
      run: async () => {
        await ok(api.PATCH("/settings/general", {
          body: {
            recommendations: features.includes("discover") || undefined,
            video_hashing: features.includes("deduplication") || undefined,
            show_collections: features.includes("collections") || undefined,
            verify_files: features.includes("brokenFiles") || undefined,
          },
        }));
        if (features.includes("transcoding")) {
          const current = await ok(api.GET("/settings/transcode"));
          await ok(api.PUT("/settings/transcode", { body: { ...current.global, enabled: true } }));
        }
      },
    });
  }
  return tasks;
}

async function makeLibrary({ state, commit }: TaskHooks, key: string): Promise<void> {
  let library = libraryOf(state(), key)!;
  let id = libraryIdOf(library);
  if (id === undefined) {
    const made = await ok(api.POST("/categories", { body: { name: library.name.trim(), media_type: library.mediaType, kind: library.kind || undefined } }));
    id = made.id;
    commit(replaceLibrary(state(), key, { madeId: id }));
    library = libraryOf(state(), key)!;
  }
  const known = await ok(api.GET("/rootfolders"));
  for (const path of foldersOf(state(), library)) {
    let rootId = known.find((root) => root.path === path)?.id;
    if (rootId === undefined) {
      const added = await api.POST("/rootfolders", { body: { path } });
      if (added.error || !added.data) throw new Error(`${path}: ${detailOf(added.error, t("setup.task.folderRefused"))}`);
      rootId = added.data.id;
    }
    const attached = await api.POST("/categories/{id}/rootfolders", { params: { path: { id } }, body: { root_folder_id: rootId } });
    // A folder the library already holds answers a conflict: that is the state wanted.
    if (attached.error && attached.response?.status !== 409) throw new Error(`${path}: ${detailOf(attached.error, t("setup.task.folderNotAttached"))}`);
  }
}

async function setQuality({ state }: TaskHooks, key: string): Promise<void> {
  const library = libraryOf(state(), key)!;
  const id = library.madeId!;
  const profiles = await ok(api.GET("/qualityprofiles"));
  const profile = starterProfile(profiles, library.mediaType, library.starter);
  if (!profile) return;
  const current = await ok(api.GET("/categories/{id}/quality", { params: { path: { id } } }));
  await ok(api.PUT("/categories/{id}/quality", {
    params: { path: { id } },
    body: {
      profile_ids: [profile.id],
      cutoff_profile_id: profile.id,
      target_id: current.target_inherited ? undefined : current.target?.id,
      target_auto: current.target_auto,
    },
  }));
}

async function linkConnection({ state }: TaskHooks, instanceId: number): Promise<void> {
  const connection = state().connections.find((one) => one.instanceId === instanceId)!;
  const libraryId = libraryIdOf(libraryOf(state(), connection.libraryKey));
  await ok(api.PATCH("/arr/instances/{id}", {
    params: { path: { id: instanceId } },
    body: { name: connection.name, base_url: connection.baseUrl, category_id: libraryId, enabled: true, push_monitoring: connection.push },
  }));
}

async function startImport({ state, commit }: TaskHooks, instanceId: number): Promise<void> {
  const connection = state().connections.find((one) => one.instanceId === instanceId)!;
  const library = libraryOf(state(), connection.libraryKey)!;
  const libraryId = libraryIdOf(library);
  const options = syncOptions(connection, libraryId);
  if (library.starter === "arr" && library.libraryId === undefined) {
    // The *Arr's own quality profiles and Media Management, mapped onto what Solarr proposes for them.
    const preview = await ok(api.POST("/arr/instances/{id}/preview", { params: { path: { id: instanceId } }, body: options }));
    options.profile_map = preview.profiles.map((profile) => ({ arr_profile_id: profile.arr_profile_id, profile_id: profile.proposed_profile_id ?? null }));
    options.settings_into = preview.settings.filter((one) => one.changes.length > 0 && one.library_id === libraryId).map((one) => one.library_id);
  }
  const started = await api.POST("/arr/instances/{id}/sync", { params: { path: { id: instanceId } }, body: options });
  if (started.data) {
    commit(replaceConnection(state(), instanceId, { runId: started.data.id }));
    return;
  }
  if (started.response?.status === 409) {
    const runs = await ok(api.GET("/arr/imports", { params: { query: { instance_id: instanceId } } }));
    const running = runs.find((run) => run.state === "RUNNING");
    if (running) {
      commit(replaceConnection(state(), instanceId, { runId: running.id }));
      return;
    }
  }
  throw new Error(detailOf(started.error, t("setup.task.importRefused")));
}

export type TaskStatus = { state: "waiting" | "running" | "done" | "failed" | "skipped"; message?: string };

/** Runs what is not done yet, in order; a task whose prerequisite failed is skipped, the rest go on. Each success is written down. */
export async function runTasks(
  initial: WizardState,
  commit: (next: WizardState) => void,
  report: (id: string, status: TaskStatus) => void,
): Promise<void> {
  let current = initial;
  const hooks: TaskHooks = {
    state: () => current,
    commit: (next) => {
      current = next;
      commit(next);
    },
  };
  const failed = new Set<string>();
  for (const task of buildTasks(initial)) {
    if (current.done.includes(task.id)) {
      report(task.id, { state: "done" });
      continue;
    }
    if (task.needs.some((id) => failed.has(id))) {
      failed.add(task.id);
      report(task.id, { state: "skipped" });
      continue;
    }
    report(task.id, { state: "running" });
    try {
      await task.run(hooks);
      hooks.commit({ ...current, done: [...current.done, task.id] });
      report(task.id, { state: "done" });
    } catch (error) {
      failed.add(task.id);
      report(task.id, { state: "failed", message: error instanceof Error ? error.message : detailOf(error, t("setup.task.failed")) });
    }
  }
}
