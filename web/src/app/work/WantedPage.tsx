import { useParams } from "@tanstack/react-router";

import { useLibrary } from "@/lib/useLibrary";
import { useMediaTypes } from "@/lib/useMediaTypes";

import { PartWanted } from "./PartWanted";
import { WorkWanted } from "./WorkWanted";

/** A library's Wanted at its type's grain: Lidarr's works, or Sonarr's parts (R1.6). */
export function WantedPage() {
  const { categoryId } = useParams({ strict: false }) as { categoryId: string };
  const library = useLibrary(categoryId);
  const info = useMediaTypes().find((entry) => entry.key === library?.media_type);
  if (!library || !info) return null;
  return info.wanted_grain === "part" ? <PartWanted info={info} /> : <WorkWanted info={info} />;
}
