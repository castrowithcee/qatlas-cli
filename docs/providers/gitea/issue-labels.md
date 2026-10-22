---
description: >
  Describes the issue-label tools of the Gitea and Forgejo providers: reading, adding, replacing, removing, and
  clearing the labels of an issue or pull request, the scopes they need, how label IDs are bound to the repository
  and its organization, and the differences between the two products.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Issue labels

The `issue-labels` tools read and change the labels attached to one issue or pull request. They hold for both
providers unless a line says otherwise. Creating, changing, and deleting the labels themselves is a different tool
group.

| Tool | Effect | Provider |
| --- | --- | --- |
| `issue-labels.list` | read | both |
| `issue-labels.add` | update | both |
| `issue-labels.replace` | update | both |
| `issue-labels.remove` | update | both |
| `issue-labels.clear` | update | both |

## Scopes

Presumably `read:issue` for the read and `write:issue` for a change. The proof reads described below presumably
need `read:issue` and `read:organization`; none of this was checked against a live instance. A missing scope shows
as `not-found`.

## Targets and label IDs

- Every tool takes `repository` (`OWNER/REPO`, checked against the targets before the secret is resolved) and the
  `number` of the issue or pull request. An index may be either: unlike the issue tools, these tools do not refuse a
  pull request.
- `issue-labels.list` needs no evidence read: its path names the repository inside the targets, so the instance
  answers for that index only.
- `add`, `replace`, `remove`, and `clear` first read the issue or pull request and require the canonical repository
  of the answer to lie inside the targets.
- A label is named by its numeric ID only; names are not accepted. Before `add`, `replace`, or `remove` sends
  anything, every ID must be found among the labels of the repository and, for IDs not found there, among those of
  the repository's owner as an organization. A label of another repository or organization, an unknown ID, and an
  owner whose labels the token does not reach all end as `invalid-request` before the change, without quoting the
  ID. Each source is read in up to 20 pages of 50.
- `replace` replaces all labels; an empty list removes them all. `clear` removes all labels without a list.
  Both are changes that need a confirmation and are not listed only: the labels themselves stay.
- A result shows at most 200 labels. Names and descriptions are untrusted data.

## Product differences

- Forgejo's `add` and `replace` take `updated_at`, the time read before, to refuse the change after the issue
  changed. Gitea refuses the member.
- Both products also accept label names in the body and Forgejo a name in the `remove` path; Qatlas offers IDs only,
  because only an ID can be proven to belong to the repository or its organization.
- Forgejo's `remove` and `clear` also document a request body (`updated_at`); Qatlas sends none.
