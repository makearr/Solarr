# Tags and genre taxonomies

One mechanism for genres and labels (ADR-33, `private` plan 10 L1): every genre is a tag, and a tag belongs to a
**taxonomy** or to none.

| | Taxonomy | Who creates its tags |
|---|---|---|
| Music genres | `music` (`internal/media/music/taxonomy.go`) | the shipped tree, MusicBrainz, Wikidata, a person |
| Film and TV genres | `screen` (`internal/media/screen`), shared by TV and movies | the shipped tree, TMDB, Skyhook, TVmaze, Wikidata, a person |
| Free labels | `""` | a person |

A media type declares its taxonomy on its descriptor (`media.Descriptor.Taxonomy`). A taxonomy's tags carry its key
in their slug (`music/rock`), so two taxonomies may both have a Comedy.

## Where a provider's genre lands

`library.resolveGenre`, in order:

1. a tag of the taxonomy with that **alias** (slugified; one alias may name several tags: "Action & Adventure" is
   both Action and Adventure);
2. the tag of that **name**;
3. a new tag, spelled in title case, placed under the tag whose name is its longest whole-word ending, by name or
   alias ("gangsta rap" under Hip Hop, "alternative metal" under Metal).

A refresh replaces only the work's provider tags of that taxonomy; a person's (source `user`) are never removed.

## The shipped tree

Applied at startup once per `Taxonomy.Version` (`tag_taxonomy` records the version): missing tags are created, a
provider's tag of the same name takes the shipped spelling and place, a provider's tag named like a shipped alias
("pop music") is merged into it, and every parentless tag is placed by the ending rule. Raise the version when the
tree changes. Genres stored before they were tags (`work_classification` kind `genre`) are moved into the work's
type's taxonomy at the same startup.

## Changing the tree

Settings -> Tags (administrator): rename, move under another tag of the same taxonomy, merge, add, delete. Renaming
or merging a provider's tag keeps its old name as an alias, so the provider's next refresh lands on it. Deleting
moves its children up; a provider's genre comes back with the next refresh unless merged.

## Filtering

The works list takes `tags=12,-7`: each positive id must be carried by the work or by a tag below it
(`tag_closure`), each negative one must not. Bulk selections carry the same chips. The custom filter's `genre` field
matches taxonomy tags by name, `tag` the free labels.
