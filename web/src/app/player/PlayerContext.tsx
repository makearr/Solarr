import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

import { usePreference } from "@/lib/usePreference";

import { PLAYER_MODES, type PlayerItem, type PlayerMode } from "./playerModel";

type Player = {
  current: PlayerItem | null;
  hasPrevious: boolean;
  hasNext: boolean;
  mode: PlayerMode;
  /** Starts playing `items` at `start`; the rest queue up behind it. */
  play: (items: readonly PlayerItem[], start?: number) => void;
  next: () => void;
  previous: () => void;
  close: () => void;
  setMode: (mode: PlayerMode) => void;
};

const PlayerContext = createContext<Player | null>(null);

/** The one player of the page frame: what is queued, which one plays, and where the player sits (kept per account). */
export function PlayerProvider({ children }: { children: ReactNode }) {
  const [queue, setQueue] = useState<readonly PlayerItem[]>([]);
  const [index, setIndex] = useState(0);
  const [storedMode, setStoredMode] = usePreference<PlayerMode>("player.mode", "bar");
  const mode = PLAYER_MODES.includes(storedMode) ? storedMode : "bar";

  const play = useCallback((items: readonly PlayerItem[], start = 0) => {
    setQueue(items);
    setIndex(start);
  }, []);
  const close = useCallback(() => setQueue([]), []);
  const next = useCallback(() => setIndex((at) => (at + 1 < queue.length ? at + 1 : at)), [queue.length]);
  const previous = useCallback(() => setIndex((at) => Math.max(0, at - 1)), []);

  const value = useMemo<Player>(
    () => ({
      current: queue[index] ?? null,
      hasPrevious: index > 0,
      hasNext: index + 1 < queue.length,
      mode,
      play,
      next,
      previous,
      close,
      setMode: setStoredMode,
    }),
    [queue, index, mode, play, next, previous, close, setStoredMode],
  );
  return <PlayerContext.Provider value={value}>{children}</PlayerContext.Provider>;
}

export function usePlayer(): Player {
  const player = useContext(PlayerContext);
  if (!player) throw new Error("usePlayer needs a PlayerProvider");
  return player;
}
