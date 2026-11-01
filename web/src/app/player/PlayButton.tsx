import { Play } from "lucide-react";

import { t } from "@/lib/i18n";
import { Button } from "@/ui/Button";

import { usePlayer } from "./PlayerContext";
import { isPlayableExtension, type PlayerItem } from "./playerModel";

/** Plays one file in the built-in player; nothing for a file that is not audio or video. */
export function PlayButton({ extension, item, className }: { extension: string | undefined; item: PlayerItem; className?: string }) {
  const player = usePlayer();
  if (!isPlayableExtension(extension)) return null;
  const label = `${t("player.play")} ${item.title}`;
  return (
    <Button variant="icon" className={className} title={label} aria-label={label} onClick={() => player.play([item])}>
      <Play aria-hidden className="size-3.5" />
    </Button>
  );
}
