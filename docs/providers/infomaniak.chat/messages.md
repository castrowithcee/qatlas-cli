---
description: >
  Describes the Infomaniak kChat message, direct and group channel, pin, and flag tools with their binding and
  behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-28
---

# Infomaniak kChat messages

The messages tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- `pins.list` and `flagged.list` bind `channel_id` like a channel tool. `messages.list`, `.thread`, `pins.list`,
  and `flagged.list` with a `channel_id` drop posts of another channel.
- A reply confirms, by reading the named `root_id`, that the root post belongs to the same `channel_id`.
- Opening a direct or group channel returns the existing one. Every named user is proven a member of a bound
  team first, the own user is added by Qatlas, and repeated IDs count once. A connection with a channel
  allow-list opens no channel, refused before any secret is read; a channel kChat returns that the connection
  excludes is refused without naming it.
- `messages.pin` and `.unpin` are idempotent, send no body, change nothing else, and report the state kChat
  confirmed; `messages.flag` and `.unflag` send one fixed `flagged_post` preference entry, no other;
  `messages.remind` (not idempotent) sends just `target_time` in Unix seconds, from an RFC 3339 time within a
  year.
- `messages.delete` soft-deletes the post, which users cannot restore; deleting a thread root also removes
  its replies.

The token rights are listed under [Messages](setup.md#messages).
