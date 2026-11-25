# Privacy and security

> **Level: 🟢 Beginner.** Solarr does not phone home. It does not even know where home is.

## Feature set

- **No telemetry, no analytics, no crash reporting, no accounts, no update check.**
- **Contacts only what you configure:** the metadata providers enabled for your libraries, your indexers, your download
  clients, your media servers and, if you use the import, your \*Arr instances.
- **The one optional extra**, a check of your public IP (to see whether traffic leaves past a VPN), runs only while you
  show the address in the status bar (hidden by default), or on a schedule if you turn it on.
- **Secrets encrypted at rest** (API keys, passwords) and never returned by the API.
- **An outbound proxy** (`http`, `https`, `socks5`) for everything, if you want one.
- **Read-only and observe modes** for trying Solarr on a library you care about.

## Benefits

- **Your library is your business.** What you collect stays on your server.
- **Nothing to opt out of**, because nothing was opted in.
- **Verifiable.** Do not take our word for it: every outbound request passes through one package, so it is one place to read.

## Technical details

- **One door out:** `internal/platform/httpx`. Code that tries to use bare HTTP fails the linter.
- **SSRF guard** on every destination, per-provider pacing and circuit breakers.
- **Encryption key:** generated on first run, or set your own with `SOLARR_ENCRYPTION_KEY` (keep it safe).
- **Setup claim protection:** the first administrator can only be created once; from a public address it needs the
  setup code printed in the log.
- **Found a vulnerability?** Please report it privately: see [SECURITY.md](../../SECURITY.md).

Further reading: [outbound requests](../outbound-requests.md), [users and access](../users-and-access.md),
[environment variables](../environment-variables.md), [Solarr mode](../solarr-mode.md).
