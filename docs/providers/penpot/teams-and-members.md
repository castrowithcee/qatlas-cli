---
description: >
  Describes the Penpot tools that list and change team members and create and delete teams: the team binding, the
  allow-list refusal, roles, and personal data.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Penpot: teams and members

`teams.list` describes the team as Penpot returns it, with the permissions of the token's account. Penpot returns
only teams that are not deleted there; the `deleted-at` field would always be empty and is not returned. The
organization's image addresses are publicly retrievable or of unproven kind; they count as access values and are not
returned. Subscription and organization appear only when the instance has the `subscriptions` and `admin-console`
flags.

The five tools administer teams. `team_id` must be a bound team (checked before the credential is resolved and before
any request; the refusal never names the team). **There is no wildcard target and Qatlas never adds a target**: the
team that `teams.create` makes is outside every connection until you add `team/ID` to a connection yourself.

- `members.list` answers at most 200 members; `id` is used as `member_id`. Email addresses, names, and photo
  identifiers are personal data and untrusted provider data: the tool has its own sensitivity class,
  `penpot-members`. The role stays derived from the membership flags, which are returned as well.
- `members.set-role` sets `admin`, `editor`, or `viewer`. The role `owner` is not offered, so ownership cannot be
  transferred.
- `members.remove`: the member must belong to the given team; Penpot looks the ID up in that team only.
- `teams.create` takes a `name` of 1 to 250 characters, none of `.`, `:`, `/`, which Penpot refuses. The new team is
  owned by the token's account.
- `teams.delete`: Penpot marks the team and its projects and files as deleted and removes them after the deletion
  delay. The roles each tool needs are in [Rights per tool group](setup.md#rights-per-tool-group).

Team and member changes affect the whole team, so **a connection with a project or file allow-list refuses all of
them, and `teams.create`**; `members.list` is still allowed unless the connection is bound to files only. Changes
follow [Changes](README.md#changes), except that an unreadable answer makes the outcome uncertain only for
`teams.create`; list the members or teams before trying again. Emails are never part of an error message.
