# Git hooks

Versioned so a fresh clone gets them. Enable once per clone:

```bash
git config core.hooksPath .githooks
```

| Hook | Does |
|------|------|
| `pre-commit` | Refuses to commit local-only files (`private/`, `CLAUDE.md`, `config.yml`, `Makefile.local`, `*.db`) even when `git add -f` staged them |

**Committing does not bump the version** — only a successful `make image` does; milestones
are names set with `make milestone NAME="…"`. See `documentation/versioning.md`.
