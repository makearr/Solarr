# Git hooks

Versioned so a fresh clone gets them. Enable once per clone:

```bash
git config core.hooksPath .githooks
```

| Hook | Does |
|------|------|
| `pre-commit` | Nothing currently — commits do not change the version |

**Committing does not bump the version.** The build number advances only on a successful
`make image`, and milestones are advanced by hand with `make milestone`. See
`documentation/versioning.md`.
