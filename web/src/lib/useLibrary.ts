import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";
import type { components } from "@/api/schema";

type Library = components["schemas"]["Library"];

/** One library by its category id, from the shared ["libraries"] query; undefined while it loads. */
export function useLibrary(categoryId: string | undefined): Library | undefined {
  const libraries = useQuery({
    queryKey: ["libraries"],
    queryFn: async () => {
      const { data, error } = await api.GET("/libraries");
      if (error) throw error;
      return data;
    },
  });
  return libraries.data?.find((library) => String(library.id) === categoryId);
}
