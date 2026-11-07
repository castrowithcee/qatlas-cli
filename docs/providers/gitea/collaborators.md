---
description: >
  Describes the collaborator tools of the Gitea and Forgejo providers: listing, checking, reading the permission of,
  adding, and removing the collaborators of a repository; target binding, the permission values, what is not offered,
  and limits.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Collaborators

These tools show and change who has direct access to one repository. Access that comes through a team or an
organization is not a collaboration and is read with [Teams](teams.md). Names and texts in answers are untrusted
personal data, in the class of `accounts.me`.

## Binding

- `repository` is chosen against the connection's targets and exclusions before the token is resolved or a request
  is sent; it may only be left out when the targets name exactly one repository.
- `collaborator` is a user name, validated as a path segment before anything is sent.
- `collaborators.list` takes `limit` and `cursor` ([Pagination](README.md#pagination)) and returns every member of
  the user object under `collaborators`.
- `collaborators.check` answers a boolean. Its HTTP 404 is the documented "no" and is reported as `false`; the
  instance gives the same 404 for a user that does not exist or a repository the token does not reach, so `false`
  proves neither. Other refusals, including 403, stay errors.
- `collaborators.get-permission` returns `permission`, `role_name`, and the `user` object. `permission` is text:
  Gitea names `none`, `read`, `write`, `admin`, or `owner`; Forgejo documents no values. The instance serves it to an
  administrator of the repository and to the user asked about. A user that does not exist answers 404 as
  `not-found`.

## Changes

- `collaborators.add` takes `permission`, required, one of `read`, `write`, or `admin`; the instance's default is not
  used. A collaborator that exists gets the new permission. Only an existing user of the instance can be added: no
  invitation, no email address, and no account is created. The instance answers 404 for an unknown user and 422 for
  the owner of the repository.
- `collaborators.remove` ends the direct access of one user. The user and the repository stay; access through a team
  or because the repository is public stays.
- Both need an administrator of the repository and send exactly one request, never repeated, also not after a server
  error or a timeout; the message then says that the change may have been applied. Both are idempotent and need
  confirmation.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `collaborators.list`, `collaborators.check`, `collaborators.get-permission` | read | both |
| `collaborators.add`, `collaborators.remove` | update | both |
