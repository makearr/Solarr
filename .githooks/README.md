# Git hooks

Versioned so a fresh clone gets them. Enable once per clone:

```bash
git config core.hooksPath .githooks
```

| Hook | Does |
|------|------|
| `pre-commit` | Bumps the patch number in `VERSION` and stages it |

`VERSION` follows `0.0.x`. Only the patch number moves automatically; major and minor are
changed by hand.

To freeze the version — during design work, or a series of commits that should share one
number — create `.version-hold` in the repo root. It is gitignored. Delete it to resume.
