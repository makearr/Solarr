import { useLibraries, type LibraryEntry } from "./resources";

/** One library, or further media type of one, by its category id, from the shared ["libraries"] query; undefined while
 *  it loads. */
export function useLibrary(categoryId: string | undefined): LibraryEntry | undefined {
  return useLibraries().data?.find((library) => String(library.id) === categoryId);
}
