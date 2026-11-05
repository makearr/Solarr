# Outbound request lanes

Every outbound call goes through `httpx`, one `Client` per provider on a shared limiter. When a provider is busy
(its rate or its concurrency), waiting requests queue in three lanes:

| Lane | What is in it | How a request gets there |
|---|---|---|
| User | Anything a web-interface request causes, including what a person is viewing | `userPriority` middleware in `apiserver` |
| Fresh | Items wanted and released in the last 48 hours or due within 7 days, RSS sync, the hunt's new-release share | `works.RefreshJob` and the artist refresh mark a work from `library.RefreshCandidate.Fresh`; RSS and the hunt mark their own context |
| Background | Everything else; a request nobody marked | The default (`httpx.PriorityUnset`) |

Each lane gets its weight over the sum of the weights of the lanes waiting (default 70/20/10, **Settings → Priority →
Outbound requests**). A lane nobody waits in gives its part to the others, so a user hammering the interface cannot shut
the other two out, and idle capacity is never held back. The same turns decide who gets the next rate token and the
next concurrency slot (`lanes.go`, `gate.go`); within a lane the more urgent request goes first, then the oldest.

`httpx.WithPriorityAtLeast` never demotes a request: MusicBrainz's finer ladder (`musicbrainz.Priority`) ranks inside a
lane and cannot pull a user's request out of the first one.

"Fresh" reads the hunt queue's release dates, so an episode only the provider knows about becomes fresh once a refresh
has stored it.
