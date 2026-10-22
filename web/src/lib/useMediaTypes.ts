import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";
import type { components } from "@/api/schema";

export type MediaTypeInfo = components["schemas"]["MediaTypeInfo"];

const NONE: readonly MediaTypeInfo[] = [];

/** The media types this build can manage, in the server's order; empty while it loads.
 *  A library can be created for exactly these. */
export function useMediaTypes(): readonly MediaTypeInfo[] {
  const mediaTypes = useQuery({
    queryKey: ["mediaTypes"],
    staleTime: Infinity,
    queryFn: () => ok(api.GET("/mediatypes")),
  });
  return mediaTypes.data ?? NONE;
}

/** One media type's description; undefined while it loads or for a type this build does not bring. */
export function useMediaType(key: string | undefined): MediaTypeInfo | undefined {
  return useMediaTypes().find((type) => type.key === key);
}
