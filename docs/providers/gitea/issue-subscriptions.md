---
description: >
  Describes the subscription tools of the Gitea and Forgejo providers: listing the subscribers of an issue or pull
  request, checking the own subscription, and subscribing or unsubscribing a user.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Issue subscriptions

The `issue-subscriptions` tools hold for both providers. Every tool takes `repository` (`OWNER/REPO`), checked
against the targets before the secret is resolved; it may be left out when the targets name exactly one repository.
Watching a whole repository is not part of this group.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-subscriptions.list`, `issue-subscriptions.check` | read | both |
| `issue-subscriptions.add`, `issue-subscriptions.remove` | update | both |

## Scopes

Reads presumably need `read:issue`, changes presumably `write:issue`. The scopes are not confirmed against a live
instance.

## Reading

- Issues and pull requests share one index space; every tool acts on either by `number`. The index is read first
  and its canonical repository must lie inside the targets.
- `list` pages the subscribers and shows every member of each user object, which is personal data; names and texts
  are untrusted data.
- `check` answers for the account behind the token, not for another user.

## Changing

- `user` names an existing user of the instance and is validated as a user name before any request.
- `added` or `removed` reports whether the state changed; a repeat reaches the same state.
- An account that is no administrator of the instance may change only its own subscription.
- After the evidence read, exactly one request is sent. None is repeated after a timeout, an abort, or a 5xx
  answer; the error then says the change may have been applied.
