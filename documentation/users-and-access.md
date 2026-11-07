# Users and access

Settings -> User (administrators). An account has a **role**, a **grant per library**, and limits.

| Role | What it is |
|---|---|
| Administrator | Changes settings, indexers and downloaders; manages every library. |
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
tags). Lists and work pages honour it; people, collection and search pages are not filtered by it yet.
