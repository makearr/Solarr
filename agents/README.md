# Briefs for AI agents

Got an AI coding agent (Claude Code, OpenAI Codex, Qwen Code, Gemini CLI, ...) and some spare usage? These briefs turn it
into a Solarr tester. Each one is a self-contained set of instructions: point your agent at the file, answer its
questions, and you get a report you can post on GitHub with **your own account**.

| Brief | What your agent does | Touches your library? |
|---|---|---|
| [recognition-test.md](recognition-test.md) | Runs a throwaway Solarr against your library, measures how much it recognises, and lists what it got wrong (optionally also tests the \*Arr import) | **No.** Read-only mount, read-only mode |
| [recognition-benchmark.md](recognition-benchmark.md) | Measures how long recognising a library takes on your hardware, and what it costs in CPU, RAM and disk | **No.** Read-only mount, read-only mode |
| [code-review.md](code-review.md) | Reviews one area of the source code for bugs, performance traps and security vulnerabilities | No, it only reads the code |

## How to use one

Tell your agent, in its own words:

> Read `agents/recognition-test.md` in the Solarr repository (https://github.com/makearr/Solarr) and follow it.

Then stay around: the briefs make the agent **ask you** before anything that matters (which folder, what may appear in
the report). The agent writes the report to a file and prints the command to post it; **you** decide whether to post.

## Ground rules every brief repeats

1. **Read-only unless you say otherwise.** Your library is mounted read-only and Solarr runs in `read_only` mode. An agent
   never moves, renames, tags or deletes your files.
2. **No secrets in reports.** No API keys, tokens, passwords, cookies, encryption keys, IP addresses or host names.
3. **You own what gets published.** File names reveal what you collect. The agent asks whether to include real names or
   anonymise them, and shows you the report before you post it.
4. **Security vulnerabilities go private**, through [GitHub's private vulnerability reporting](../SECURITY.md), never as
   a public issue.
5. **Throwaway means throwaway.** The test instance gets its own scratch config folder and is removed at the end.

## Before you start

Please glance at the [open issues](https://github.com/makearr/Solarr/issues) so your agent does not report something
already known. If you want to point an agent at the code for something bigger than a report, open an issue first: two
agents that have never met, confidently rewriting the same file, make for an entertaining merge conflict and a terrible
afternoon.
