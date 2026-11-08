# Favorites

A heart marks something you want to find again. It is **not a follow**: following a person or a studio adds their new
work to a library and has it acquired, while a heart changes nothing about the library and downloads nothing. Hearts
are your own; another user does not see them.

## What can be hearted (🟡)

Albums, series, films and other works; songs and episodes; artists, actors and other people; studios, labels and
networks; collections; tags (a heart on a tag opens the items carrying it).

The heart sits on the cover (shown on hover until it is set), in the **Favorite** column of every table, in the
overview rows, on the track and episode rows of a work, and beside the name on a person, organisation, work or
collection page.

## Showing only favorites

The toolbar of the albums, series and film lists, the Artists and Tracks levels, People, Studios/Networks and
Collections has a **Favorites** button. It keeps the rows you hearted, in the list's own order and with its other
filters. It is part of the address, so a favorites view can be bookmarked, and a bulk action made with "select all
matching" under it touches only the favorites.

## API

`GET /api/v1/favorites?subject_type=work` answers the hearted ids; `PUT` and `DELETE`
`/api/v1/favorites/{subject_type}/{id}` heart and un-heart. The list endpoints take `favorite=true`.
