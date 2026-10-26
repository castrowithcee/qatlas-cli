---
description: >
  Describes the reaction tools of the Gitea and Forgejo providers: reading, adding, and removing the reactions of an
  issue, a pull request, or a comment, how the target is bound to the repository, and what removing means.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Reactions

The `issue-reactions` tools act on the reactions of one issue or pull request (both share one index), the
`comment-reactions` tools on those of one comment of either. They hold for both providers. Every tool takes
`repository` (`OWNER/REPO`), checked against the targets before the secret is resolved; it may be left out when the
targets name exactly one repository. Reactions are untrusted data, and the users who reacted are personal data.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-reactions.list`, `comment-reactions.list` | read | both |
| `issue-reactions.add`, `comment-reactions.add` | create | both |
| `issue-reactions.remove`, `comment-reactions.remove` | delete | both |

## Binding to the repository

- An issue or pull request is addressed by `number` under the repository path. `list` needs no further proof; `add`
  and `remove` first read the issue or pull request and require the canonical repository of the answer to lie inside
  the targets.
- A comment is addressed by `id`, which is a foreign object ID. All three comment tools read the comment first, as
  described for [comments](comments.md#binding-a-comment-id-to-its-repository); a comment of another repository is
  refused as `invalid-request` without naming it, before any reaction is read or changed.

## Adding and removing

- `content` is a free reaction of 1 to 64 characters; the instance accepts the ones its administrator allows
  (by default `+1`, `-1`, `laugh`, `hooray`, `confused`, `heart`, `rocket`, `eyes`) and refuses the rest.
- `add` sends one `POST` and is idempotent: `added` is true when the instance answered 201 and false when the account
  had already left that reaction (200).
- `remove` sends one `DELETE` with the body `content` and takes no user: the instance removes only the reaction of
  the account behind the token. It succeeds also when the account left none, and `removed` only reports that the
  instance accepted the call.
- Neither is repeated after a timeout, an abort, or a 5xx answer; the error then says the change may have been
  applied.

## Lists

- `issue-reactions.list` pages with `limit` and `cursor` like the other paged lists.
- `comment-reactions.list` is not paged by the instance; it returns at most 100 reactions and sets `truncated` when
  more exist.
- Each reaction carries `content`, `created_at`, and the `user` as a user object without the security members of the
  own account.
