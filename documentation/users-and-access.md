# Users and access

Settings -> User (administrators). An account has a **role**, a **grant per library**, and limits.

| Role | What it is |
|---|---|
| Administrator | Sees and changes settings, indexers, downloaders, *Arr connections, download folders, schedules and network; manages every library; sees jobs that belong to no library. |
| User | Works the queue; libraries are granted one by one. (Called operator before 2026-11; the first start renames it.) |
| Read only | Looks, and requests where allowed. |

| Library grant | The account may |
|---|---|
| No access | Nothing: the library is not there for it (a 404, never a 403). |
| Request | Browse, and ask for additions; an administrator approves each (Activity -> Requests). |
| Add | Like Request, but approved at once: the item is added and the request is recorded as fulfilled. |
| Manage | Add, monitor, edit and delete. |

An older view-only grant ("Read") still works and is shown as *View only (old)* where an account has one. The per-account
"Requests need no approval" switch stays: it approves an account's requests on every library it may request in.

**Hide items tagged.** An administrator can keep an account from the items carrying a tag (or a tag below it), whatever
its library access: the items leave its lists and their pages answer 404. It is set when the account is created and from
**Edit** on the account, which shows the same fields as creation (role, libraries, interface levels, new-library grant,
tags). Lists, work pages (with their files, tracks and cover), playback, search results and a person's credit list honour
it; collection pages are not filtered by it yet. A file placed in several roots plays through any root the account may read.
