# Versioning

```text
MAJOR . MINOR . BUILD
  0   .   2   .  17
  │       │       └── successful Docker builds since this milestone
  │       └────────── milestone: each working library type
  └────────────────── 1.0 when every planned library type works
```

## BUILD — successful Docker builds

Advances **only** when `make image` completes successfully. Commits do not change it, so the
number means "this many images have been produced", not "this much has been typed".

A failed build leaves the version untouched. `make image NOBUMP=1` builds without advancing —
useful when iterating on the Dockerfile itself.

## MINOR — milestones

Advanced by hand with `make milestone`, which increments MINOR and resets BUILD to 0. Each
milestone is **something that works**, not something that is written.

| Version | Milestone |
|---------|-----------|
| `0.0.x` | Pre-milestone — foundation, store, jobs, API skeleton |
| `0.1.x` | **Working web interface** |
| `0.2.x` | **Working music library** |
| `0.3.x` | Working TV library |
| `0.4.x` | Working movie library |
| `0.5.x` | Working audiobook library |
| `0.6.x` | Working book library |
| `0.7.x` | Working adult library |
| `0.8.x` | Working comic library |
| `0.9.x` | Working ROM library |
| `1.0.0` | All planned library types working |

The nine library milestones fit `0.1` to `0.9` exactly, so `1.0` means the roadmap in
`private/architecture.md` Part IV is complete.

**"Working" is a high bar.** A library type reaches its milestone when it can identify,
acquire, import and present that media end to end — not when its module compiles. The stage
checklist in `private/todo.md` is the definition.

## What the version is not

- Not a measure of effort or commits.
- Not automatically semver-meaningful below `1.0` — the API is unstable until then.

## Injecting it into the binary

Build metadata is compiled in and surfaced at `/api/v1/about`, which is also how the AGPL §13
source obligation is met (see `licence-compliance.md`):

```bash
-ldflags "-X main.version=$(cat VERSION) -X main.commit=$(git rev-parse --short HEAD) \
          -X main.buildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
```

A build with an unknown commit reports it as unknown rather than showing a plausible-looking
wrong value.
