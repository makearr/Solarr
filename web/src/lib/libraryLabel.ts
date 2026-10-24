import { useLibraries } from "./resources";
import { useMediaTypes } from "./useMediaTypes";

interface Named {
  id: number;
  name: string;
  media_type?: string;
  library_id?: number | null;
}

/** A library's name as a list of every kind shows it: each type of a mixed library named ("Anime · Movies"). */
export function useLibraryLabel(): (entry: Named) => string {
  const libraries = useLibraries().data ?? [];
  const mediaTypes = useMediaTypes();
  const mixed = new Set(libraries.flatMap((library) => (library.library_id != null ? [library.library_id] : [])));
  return (entry) => {
    const known = libraries.find((library) => library.id === entry.id);
    const head = entry.library_id ?? known?.library_id ?? entry.id;
    if (!mixed.has(head)) return entry.name;
    const mediaType = entry.media_type ?? known?.media_type ?? "";
    return `${entry.name} · ${mediaTypes.find((info) => info.key === mediaType)?.work.many ?? mediaType}`;
  };
}

/** The media types of the library categoryId belongs to, its first first, each with its category and works' name; one
 *  entry for a library of one type. */
export function useLibraryKinds(categoryId: string | undefined): { libraryId: string | undefined; kinds: { categoryId: string; label: string }[] } {
  const libraries = useLibraries().data ?? [];
  const mediaTypes = useMediaTypes();
  const entry = libraries.find((library) => String(library.id) === categoryId);
  if (!entry) return { libraryId: categoryId, kinds: [] };
  const head = entry.library_id ?? entry.id;
  const kinds = libraries
    .filter((library) => library.id === head || library.library_id === head)
    .map((library) => ({ categoryId: String(library.id), label: mediaTypes.find((info) => info.key === library.media_type)?.work.many ?? library.media_type }));
  return { libraryId: String(head), kinds };
}
