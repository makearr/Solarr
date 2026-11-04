import { Heart } from "lucide-react";

import { useFavoriteIds, useSetFavorite, type FavoriteSubject } from "@/lib/useFavorites";

/** The heart on anything that can be a favorite. It sits inside links and rows, so a press never opens them. */
export function HeartButton({ subject, id, name, className = "" }: { subject: FavoriteSubject; id: number; name: string; className?: string }) {
  const favorite = useFavoriteIds(subject).has(id);
  const set = useSetFavorite(subject);
  return (
    <button
      type="button"
      aria-pressed={favorite}
      aria-label={favorite ? `Remove ${name} from favorites` : `Add ${name} to favorites`}
      title={favorite ? "Favorite" : "Add to favorites"}
      onClick={(event) => {
        event.preventDefault();
        event.stopPropagation();
        set.mutate({ id, favorite: !favorite });
      }}
      className={`flex size-6 items-center justify-center rounded-full bg-canvas/85 shadow-sm hover:text-danger focus-visible:outline-2 focus-visible:outline-accent ${
        favorite ? "text-danger" : "text-ink-muted"
      } ${className}`}
    >
      <Heart aria-hidden className={`size-3.5 ${favorite ? "fill-current" : ""}`} />
    </button>
  );
}
