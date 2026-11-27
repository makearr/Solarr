import { useEffect, useState } from "react";

import type { components } from "@/api/schema";
import { usePreference } from "@/lib/usePreference";

export type ArrApp = components["schemas"]["ArrApp"];

export type StepId = "start" | "languages" | "connect" | "libraries" | "quality" | "tools" | "features" | "review" | "finish";
export type WizardPath = "migrate" | "fresh";
/** What a library's quality starts as: one of the three starters, the *Arr's own mapped onto Solarr's, or what the library has. */
export type Starter = "arr" | "best" | "balanced" | "space" | "keep";

export const STEPS: Record<WizardPath, readonly StepId[]> = {
  migrate: ["start", "languages", "connect", "quality", "features", "review", "finish"],
  fresh: ["start", "languages", "libraries", "quality", "tools", "features", "review", "finish"],
};

/** The optional features the assistant offers; every one is off on a fresh install. */
export const FEATURES = ["discover", "deduplication", "collections", "brokenFiles", "transcoding"] as const;
export type Feature = (typeof FEATURES)[number];

/** What an import brings in from one *Arr. */
export interface ImportChoices {
  monitored: boolean;
  unmonitored: boolean;
  indexers: boolean;
  downloadClients: boolean;
  importLists: boolean;
}

export const DEFAULT_CHOICES: ImportChoices = { monitored: true, unmonitored: true, indexers: true, downloadClients: true, importLists: true };

/** One folder the *Arr holds, and the folder that is the same place inside Solarr's container. */
export interface FolderMap {
  arrPath: string;
  here: string;
}

/** A library to make (or an existing one to use), with the folders it holds. */
export interface WizardLibrary {
  key: string;
  name: string;
  mediaType: string;
  kind: string;
  folders: string[];
  starter: Starter;
  /** Take the *Arr's naming, translated, into the library; on unless switched off in the review. */
  takeNaming?: boolean;
  /** An existing library picked to use as it is. */
  libraryId?: number;
  /** The library made at Finish, remembered so a retry does not make it twice. */
  madeId?: number;
}

export interface WizardConnection {
  instanceId: number;
  app: ArrApp;
  name: string;
  baseUrl: string;
  version?: string;
  /** The library the content comes into; absent for Prowlarr. */
  libraryKey?: string;
  folderMaps: FolderMap[];
  choices: ImportChoices;
  push: boolean;
  runId?: number;
  /** The languages the app's own profiles ask for, read when it was tested. */
  languages?: string[];
}

/** Everything the assistant remembers, on the server per account, so a reload or another browser picks up where it stopped. No secret is in it. */
export interface WizardState {
  v: 1;
  path?: WizardPath;
  step: StepId;
  languages?: string[];
  connections: WizardConnection[];
  libraries: WizardLibrary[];
  /** The optional features to switch on at Finish. */
  features?: Feature[];
  /** Run Solarr beside the *Arrs it was connected to (they keep downloading, monitoring follows them) instead of replacing them; chosen unless said otherwise. */
  companion?: boolean;
  /** The work items of the Finish step already done, so a retry does only what is left. */
  done: string[];
  finished: boolean;
}

export const EMPTY_WIZARD: WizardState = { v: 1, step: "start", connections: [], libraries: [], done: [], finished: false };

export const WIZARD_KEY = "setup.wizard";

/** The wizard's state: held locally for speed, written to the server on each change. `ready` is false until the stored one is read. */
export function useWizardState(): { state: WizardState; ready: boolean; update: (next: WizardState) => void; reset: () => void } {
  const [stored, setStored, loaded] = usePreference<WizardState | null>(WIZARD_KEY, null);
  const [local, setLocal] = useState<WizardState | null>(null);
  useEffect(() => {
    if (loaded && local === null) setLocal(stored?.v === 1 ? stored : EMPTY_WIZARD);
  }, [loaded, stored, local]);
  const update = (next: WizardState) => {
    setLocal(next);
    setStored(next);
  };
  return { state: local ?? EMPTY_WIZARD, ready: local !== null, update, reset: () => update(EMPTY_WIZARD) };
}

/** The steps shown in the stepper: the path's own once chosen, before that the two every path starts with. */
export function stepsOf(path: WizardPath | undefined): readonly StepId[] {
  return path ? STEPS[path] : STEPS.migrate.slice(0, 1);
}

/** The languages the connected apps' profiles ask for, each once, in connection order. */
export function suggestedLanguages(state: WizardState): string[] {
  return [...new Set(state.connections.flatMap((connection) => connection.languages ?? []))];
}

export function libraryOf(state: WizardState, key: string | undefined): WizardLibrary | undefined {
  return state.libraries.find((library) => library.key === key);
}

export function replaceLibrary(state: WizardState, key: string, change: Partial<WizardLibrary>): WizardState {
  return { ...state, libraries: state.libraries.map((library) => (library.key === key ? { ...library, ...change } : library)) };
}

export function replaceConnection(state: WizardState, instanceId: number, change: Partial<WizardConnection>): WizardState {
  return { ...state, connections: state.connections.map((one) => (one.instanceId === instanceId ? { ...one, ...change } : one)) };
}
