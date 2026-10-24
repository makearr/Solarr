# Libraries of several media types

A library may hold more than one media type (plan 10-libraries L4): an Anime library holding series and movies.

## Storage

The library is its first category; each further media type is a **member** category whose `category.library_id`
names the first. Scan, import, hunt, naming, quality and every per-library setting see ordinary single-type
categories, so none of them knows about mixing. What makes them one library:

- `settings.Cache.AddLibraryType` creates a member with the library's name, priority, rating limit, search share
  and every account's access; `SyncLibrary` copies them again after the library changes (`UpdateCategory`, the
  priority page). A member's own edit keeps the library's name and priority; a rating limit set on it is the library's.
- Access to a library is access to every member (`SetCategoryAccess`, `RevokeCategoryAccess`).
- A library with members refuses deletion; remove the members first. Portable configuration writes `library_of`.

## Interface

- `GET /libraries` answers each library once, its members inside (`members`, full `Library` objects with their own
  ids and levels); the web's `useLibraries()` flattens them (`library_id` set on a member), `useLibraryTree()` keeps
  the tree.
- The sidebar shows one entry: **All** (`/library/{first}/all`), then the levels of each type, a repeated label
  suffixed with the type ("Cast · Movies"); Wanted, Insights, Files and Settings get a tab per type.
- The All list is the works list with `whole_library=true` (every member's category) and an optional `media_type`
  (the type chip); bulk selections carry both.
- `useLibraryLabel()` names a member "Anime · Movies" wherever libraries of every type are listed together.
