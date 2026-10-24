---
description: >
  Describes the Gitea and Forgejo user tools: searching and reading users, their activity feeds and contribution
  heatmaps, and followers, following, and follows. It covers the token scopes, how views of other people and of the
  account behind the token are bound to the targets, the personal-data rules, differences between the products, and
  the limits.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Users

These tools read the profiles of users, their activity, and who follows whom, and change which users the account
behind the token follows. Names, profile texts, and locations in answers are untrusted personal data; profile
addresses (`website`, `avatar_url`) are shown and never fetched. [Forgejo](../forgejo.md) names the differences.

## Scopes

A read needs `read:user`, `user-follows.add` and `user-follows.remove` need `write:user`. Both products serve the
routes in the `user` category.

## Target binding

- `users.search`, `users.get`, `user-activities.list`, `user-heatmap.get`, and the follower, following, and check
  tools with a `username` show other people. They reach past any target, so only a connection without targets offers
  them; with targets they are refused before the token is read, and the refusal names no user.
- The follower and following lists, `user-following.check` without `username`, and `user-follows.add` and
  `user-follows.remove` belong to the account behind the token. With targets, the connection must name that account as
  `users/NAME`; `orgs/NAME` and `repos/OWNER/REPO` do not allow it, and a repository target refuses it as it does
  `accounts.me`. A read of the account (`GET /user`) first proves that the named user is the token's account. Without
  targets there is no proof read. The lists still show other people and carry their personal data.
- A follow change is that one proof read (with targets) followed by exactly one change request, which is never
  repeated, also not after a server error or a timeout.

## Users

- The user object keeps the person members. The sign-in name at the authentication source, the source ID, the
  prohibition of signing in, and the time of the last sign-in are left out; `is_admin` and `restricted` stay.
- `users.search` filters by `q` and `uid`, and sorts only on Forgejo (`sort`). The answer is a
  `{"ok":true,"data":[...]}` wrapper. The instance hides the search from users who are not signed in when it
  requires sign-in to explore, and can switch it off.
- `user-heatmap.get` returns at most 400 entries and sets `truncated` beyond that. An instance can switch the heatmap
  off; the route is then missing and the answer is `not-found`.
- `user-following.check` reports a 404 as `false`, although the instance also answers it for a user the token does
  not reach; any other failure is an error. `target` is the checked user; `username` is whose follows are asked.
- `user-activities.list` filters by `date` and `only_performed_by`.

## Differences

- The user object carries `pronouns` on Forgejo and `type` on Gitea 28 and later.
- `sort` of `users.search` exists on Forgejo only; Gitea refuses it as an invalid argument.
