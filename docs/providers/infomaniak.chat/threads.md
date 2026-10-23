---
description: >
  Describes the Infomaniak kChat followed thread tools with their binding and behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat threads

The threads tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- A `thread_id` (`threads.get`, `.follow`, `.unfollow`, `.mark-read`) is a root `post_id`: it is bound like
  one, must not be a reply, and its channel must be in the reachable set of the bound `team_id` described
  in [Scope](README.md#scope), so a thread of another bound team is refused.
- `threads.follow` and `.unfollow` are idempotent, send no body, change nothing else, and report the state
  kChat confirmed. Threads never include deleted threads or participants' user data.
- `threads.list` reports `total` as kChat's count before the reachable-channel filter.
