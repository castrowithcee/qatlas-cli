---
description: >
  Describes the pin tools of the Gitea and Forgejo providers: listing pinned issues, checking whether new pins are
  allowed, and pinning, unpinning, and moving the pin of an issue or pull request.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Issue pins

The `issue-pins` tools hold for both providers. Every tool takes `repository` (`OWNER/REPO`), checked against the
targets before the secret is resolved; it may be left out when the targets name exactly one repository.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-pins.list`, `issue-pins.check-allowed` | read | both |
| `issue-pins.pin`, `issue-pins.unpin`, `issue-pins.move` | update | both |

## Scopes

Reads presumably need `read:issue`, changes presumably `write:issue`. The scopes are not confirmed against a live
instance.

## Reading

- `list` shows pinned issues only; pinned pull requests are read by `pull-requests.list-pinned`. The instance does
  not page this list: at most 100 entries are shown and `truncated` tells when more exist. Entries whose canonical
  repository lies outside the targets are dropped. Titles, bodies, and names are untrusted data.
- `check-allowed` answers separately for issues and for pull requests whether the repository accepts another pin.

## Changing

- Issues and pull requests share one index space, so `pin`, `unpin`, and `move` act on either by `number`. `move`
  takes `position`, a positive integer.
- The issue or pull request is read first and its canonical repository must lie inside the targets; only then
  the single change is sent. A repository, index, or position that fails the check is refused before any request.
- The instance documents only 204 for all three changes and no answer for a pin that already exists or does not,
  so a repeat reaches the same state and the changes are declared idempotent.
- None is repeated after a timeout, an abort, or a 5xx answer; the error then says the change may have been
  applied.
