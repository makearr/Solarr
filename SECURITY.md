# Security policy

## Reporting a vulnerability

Please **do not open a public issue** for a security vulnerability. Report it privately instead:

**https://github.com/makearr/Solarr/security/advisories/new**

Include what you can: the affected area (file and line if you know it), how to trigger it, what an attacker gains, and
who could exploit it (anyone on the network, a signed-in user, an administrator). A proof of concept helps a lot.
Reports written by an AI agent are welcome; the [code review brief](agents/code-review.md) produces one in the right shape.

You will get an answer as soon as the one-person team behind Solarr can manage, and credit in the advisory if you want it.

## Supported versions

Solarr is an early alpha. Fixes go into the next build on the `dev` branch and the next release; there are no
backports to older releases.

## Scope

In scope: the Solarr server, its web interface, the Docker image, and the provider definitions shipped with it.
Out of scope: the services Solarr talks to (indexers, download clients, media servers, metadata providers), and set-ups
that deliberately switch off authentication (`SOLARR_AUTH_MODE=none` serves everyone as administrator, as documented).
