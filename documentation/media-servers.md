# Media servers

Connections > Media servers tells Solarr about the servers that play what it manages. Each connection is tested before it is saved, and every call goes out through `httpx` with the connection's own host admitted (a LAN address included) and every other destination classified as usual.

## Kinds

| Kind | Credential | Push | Watch state | Notes |
|---|---|---|---|---|
| Jellyfin | API key, or user name and password (a password may be empty) | Targeted `Media/Updated`, a library refresh above 50 paths in one batch; "Now available" messages | Per user, with file paths and provider ids | Sign-in sends `Authorization: MediaBrowser ...`; newer servers refuse `X-Emby-Authorization` |
| Audiobookshelf | API key | One library scan per mapped library | Per user (an administrator's key lists them) | Items are read one by one for their path |
| Kavita | API key, or user name and password | One library scan per mapped library | Not read | Users list needs an administrator |
| Navidrome | User name and password | One incremental `startScan`, skipped while a scan runs | The signed-in user's albums, newest first | Lists no library paths, so path mapping is manual |
| Calibre | Content server address | None | None | |
| Stash | API key, or user name and password (a session cookie) | None | Not read | |

## Path mapping

A server often sees the same folders under another prefix (a container mount). Each connection holds pairs of Solarr prefix and server prefix; the connection's Test checks that the server's library paths are found. Paths are rewritten both ways: Solarr to the server when it pushes, the server to Solarr when it reads watch state.

## What is sent

Each seam that places, moves, renames, recycles or upgrades files records the work's folders into `media_server_pending` (library.db), one row per connection that takes files of the library, keyed by folder and change. The `media_server_push` task (every minute) and a delayed job queued by each record send the rows older than `media_server.push_debounce_seconds` (default 30). A failure keeps the rows and counts an attempt; after ten attempts a row is dropped and the connection's last error says so. The push job is mutating, so the dry-run gate holds it. Deleting a connection drops its rows.

Seams: tv, movie, book and adult importers (`AfterPlacing`), music (`pushingCommitter`), move and rename jobs (on finish), the recycle bin (per file). The audiobook importer is not wired in the application yet, so audiobook placing does not push.

## Watch state

`media_server_playback` (every 30 minutes, or Read watch state now on the connection) reads each user's plays newest first, since the last stored play of that user. Each item is resolved to a work and part by its mapped file path first, then by provider id; items that match nothing are counted and skipped. A server that reports only a position counts as played at `media_server.played_percent` (default 90). State is kept by work and part, not by file, so an upgrade keeps it. The high-water mark of a user moves only after every page of that user is stored.

People are the server's accounts. Each starts counted and unmapped; mapping one to a Solarr account lets that account see its own state, and unchecking Counted keeps the account out of "everyone watched it".

Who sees what: a mapped user sees their own state; an administrator without a mapping sees "watched by N of M counted users"; anyone else sees nothing.

## The Watched list

Libraries > Watched (administrator, advanced tier) lists works every counted user of the connections that map the library has played, whose last play is at least the chosen number of days ago (30 by default, kept in the page's address), and that still have files. Choose rows, then Recycle files or Recycle and unmonitor; both preview the works, files and size first. Files go to the recycle bin and can be restored there. Recycle files leaves the works monitored, so Solarr may look for them again.
