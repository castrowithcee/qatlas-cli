---
description: >
  Describes the assignee tools of the Gitea and Forgejo providers: listing the assignable users of a repository and,
  on Gitea, checking, adding, and removing the assignees of one issue; their scopes, target and index binding,
  product differences, and limits.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Assignees

The assignee tools read who can be assigned in a repository and, on Gitea only, change the assignees of one existing
issue. Forgejo and Gitea both set assignees through the members of `issues.create` and `issues.update`; the tools
here are the separate routes.

## Scopes

A read presumably needs `read:issue` (and `read:repository`), a change presumably `write:issue`; none was checked
against a live instance. A change also reads the issue first.

## Binding

- `repository` is chosen against the connection's targets before the token is resolved or a request is sent; it may
  only be left out when the targets name exactly one repository.
- `issue-assignees.*` act on an issue index. The issue is read first: the answer must name a repository inside the
  targets and must not be a pull request, otherwise the call is refused as `invalid-request` before anything changes.
- User names are validated as path segments or body values (one to ten per change, no duplicates, compared without
  case). Nothing else is sent.

## Differences

- `assignees.list` exists on both products and returns every member of the user object, without paging; a
  list longer than the bound is cut and marked `truncated`.
- The check tools and `issue-assignees.*` exist on Gitea only and are not registered for Forgejo.
- A check answers a boolean. Its HTTP 404 is the documented "no" and is reported as `false`; the instance gives the
  same 404 when the token does not reach the repository, so `false` does not prove the user is unassignable. Other
  refusals, including 403, stay errors.
- Adding or removing returns the changed issue; both are idempotent.

## Bounds

User objects are personal data, with names and texts untrusted; the data class is the one of the issue tools.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `assignees.list` | read | both |
| `assignees.check`, `issue-assignees.check` | read | Gitea |
| `issue-assignees.add`, `issue-assignees.remove` | update | Gitea |
