---
description: >
  Describes the Infomaniak kChat channel, archived channel, channel member, sidebar category, and channel
  notification tools with their binding and behaviour rules.
type: knowledge
edit: shared
created: 2026-09-27
updated: 2026-10-23
---

# Infomaniak kChat channels

The channels tools of the [Infomaniak kChat provider](README.md). The effects and tool lists are in the
[tool overview](README.md#tools); the target checks are described under [Scope](README.md#scope).

- `channels.get` takes `channel_id` or `team_id` with `name`; the channel kChat answers with must belong to a
  bound team and be inside the allow-list. `channels.browse` drops channels outside the allow-list.
- `channels.update`, `.archive`, `.restore`, `.privacy`, and the `channel-members` tools bind their channel
  with the live check and refuse direct and group channels; `channels.update`, `.archive`, and `.privacy`
  also refuse archived channels, and `.restore` accepts only archived ones. `channel-members.add` proves each
  user a current member of the channel's own team, not merely of some bound team.
- `archived-channels.list` takes a bound `team_id`, checked locally, and drops channels of other teams,
  outside the allow-list, and direct and group channels.
- The `categories` tools take a bound `team_id`, checked locally, and address the own user. A category
  must belong to that team and user. Shown `channel_ids` are only the reachable channels of the search set;
  any other channel in `channel_ids` is refused before the change, outside the allow-list locally.
  `channel-notifications.update` binds its channel like `channels.update`.
- `channels.create` works only in a bound team and only on a connection without a channel allow-list,
  because a channel that does not exist yet cannot be inside one; the refusal is local.
- `channels.create` and `.update` never create or change a direct or group channel; `update` sends only the
  given fields. Re-adding a member or reaction changes nothing; kChat notifies added users.
- `categories.update` changes only the `display_name` of a custom category and its reachable `channel_ids`;
  other properties are written back as read, and channels the connection cannot reach stay in the category,
  after the given ones. System categories accept only `channel_ids`; `categories.create` and `.delete` handle
  only `custom` ones. Category order is not offered.
- `channel-notifications.update` sends only the given fields and no direct or group channel.
- Read state is always the token's own, never an argument, and the marks are idempotent. `channels.unread`
  returns only the counts of the bound channel. `channels.mark-read` is kChat's channel view, so it also
  clears push notifications for the channel; its body holds `channel_id` only.
