# wip

Tracks parallel workstreams as plain Markdown files, for a human and their agents.

Each workstream is one `ID.md` file in `$WIP_DIR` (default `~/.local/share/wip`): YAML
frontmatter for state, a free-form notes section, and an append-only dated log. Done
and dropped items are moved to `archive/` by `wip prune`. Nothing is ever deleted.

```markdown
---
title: Speed up api reconcile loop
scope: work                 # free-form, for filtering
state: waiting              # active | waiting | done | dropped
waiting_on: alice           # me | <who>, only when waiting
status: PR up, needs review # ≤ 80 chars
blockers:
  - staging cluster down
repos:                      # plain names, for filtering
  - api
paths:                      # drives `wip here`: a worktree, or a whole repo
  - ~/work/api/perf
issue: https://github.com/acme/tracker/issues/1234
doc: ~/work/api/perf/HANDOFF.md
created: 2026-10-01
updated: 2026-10-13
---

Free-form notes.

## Log

- 2026-10-13 waiting on alice: PR up, needs review
- 2026-10-10 profiled; hot path is label selector matching
```

Every write goes through the CLI, which validates the result, takes a lock on the
store, and replaces the file atomically. Unknown fields, bad enums and over-long
statuses are rejected.

## Install

Linux amd64/arm64 binaries are attached to each GitHub release:

```sh
mise use -g github:guille/wip
```

## Usage

See `wip help`. Common commands:

```sh
wip ls --scope work                 # open items
wip ls --waiting me                 # what needs me
wip here                            # items bound to this directory
wip report --since mon --scope work # standup material
wip new "Title" --scope work --here
wip log ID "PR opened"
wip wait ID alice "review"
wip done ID
```

Output is coloured on a terminal (`NO_COLOR` turns it off) and plain otherwise.

zsh completion: `wip completion zsh > /usr/local/share/zsh/site-functions/_wip`.

## Agents

`wip here` prints nothing outside bound directories, which makes it a good
SessionStart hook. Have agents write through the CLI rather than editing the
files, so every change is validated.

## Dev

```sh
mise run check    # fmt + vet + fix + golangci-lint
mise run test
```
