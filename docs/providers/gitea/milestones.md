---
description: >
  Describes the milestone tools of the Gitea and Forgejo providers: target and ID binding, and the limits of create,
  update, and delete.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Milestones

The milestone tools read and change the milestones of one repository inside the connection's targets. Both products
serve them with the same shape, so every tool exists for Forgejo and Gitea. The assignment of a milestone to an issue
is set by `issues.create` and `issues.update`.

## Binding

Every tool takes `repository` (`OWNER/REPO`), checked against the targets before the secret and any request; it may
be left out only when the targets name exactly one repository. A milestone object carries no repository, so a milestone ID is bound by an evidence read: `get`, `update`, and
`delete` first read `GET .../milestones/{id}` on the repository-bound path, which the instance answers with not found
for a milestone of another repository, and require the answer to carry the requested ID. Only then does `update` or
`delete` send its single change request; a failed read sends none.

## Behavior

- `id` is the numeric ID that `milestones.list` reports. Gitea also accepts a name in the path; the tools take the
  ID only.
- `due_on` is an RFC 3339 time. Neither product documents a way to remove a due time, so the tools offer none.
- `state` is `open` or `closed`; the list filter also takes `all`.
- Deleting a milestone removes it from its issues and cannot be undone.
- Titles and descriptions are untrusted data.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `milestones.list`, `milestones.get` | read | both |
| `milestones.create` | create | both |
| `milestones.update` | update | both |
| `milestones.delete` | delete, listed only | both |
