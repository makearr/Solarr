import { useState } from "react";

import { CoverArt } from "@/app/CoverArt";

export type ImageShape = "cover" | "poster" | "photo" | "logo";

const SHAPES: Record<ImageShape, string> = {
  cover: "aspect-square rounded-lg object-cover",
  poster: "aspect-[2/3] rounded-lg object-cover",
  photo: "aspect-square rounded-full object-cover",
  logo: "aspect-[3/2] rounded-lg bg-white object-contain p-3",
};

/** One picture of an entity in its shape: Solarr's own artwork, else a provider's URL, else the title's initial. */
export function Image({
  shape,
  title,
  artwork,
  src,
  className,
}: {
  shape: ImageShape;
  title: string;
  /** Solarr's artwork endpoint, versioned so a picked image shows at once. */
  artwork?: { subject: "works" | "people"; id: number; size?: "thumb" | "small" | "full" };
  src?: string;
  className?: string;
}) {
  const [failed, setFailed] = useState(false);
  const classes = `w-full border border-border-subtle bg-surface ${SHAPES[shape]} ${className ?? ""}`;
  if (src && !failed) {
    return <img src={src} alt={title} loading="lazy" className={classes} onError={() => setFailed(true)} />;
  }
  if (artwork) {
    return <CoverArt subject={artwork.subject} workId={artwork.id} title={title} size={artwork.size ?? "small"} className={`${classes} text-2xl`} />;
  }
  return (
    <span aria-label={title} className={`flex items-center justify-center text-2xl text-ink-muted ${classes}`}>
      {title.trim().charAt(0).toUpperCase() || "?"}
    </span>
  );
}
