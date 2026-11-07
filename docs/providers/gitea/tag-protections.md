---
description: >
  Describes the tag protection tools of the Gitea and Forgejo providers: listing, reading, creating, changing, and
  deleting the tag protection rules of a repository; target binding, rule IDs, name patterns, allowlists,
  confirmation, and limits.
type: knowledge
edit: shared
created: 2026-11-07
updated: 2026-11-07
---

# Tag protections

These tools show and change the tag protection rules of one repository. A rule has a numeric `id` and a
`name_pattern` such as `v*`; it covers every tag the pattern matches, and only the users and teams in its allowlists
may create and delete such tags. No tool here creates or deletes a tag ([Tags](tags.md)), and branch protection is
another tool group ([Branch protections](branch-protections.md)). Name patterns and the user and team names in the
allowlists are untrusted data. The tools need a token that is an administrator of the repository.

## Binding

- `repository` is chosen against the connection's targets and exclusions before the token is resolved or a request
  is sent; it may only be left out when the targets name exactly one repository.
- `id` of `get`, `update`, and `delete` must be a positive integer, as `tag-protections.list` reports it. Anything
  else is refused as `invalid-request` before the token is resolved. It is the only value sent in the path.
- `name_pattern` is checked before anything is sent: not empty, at most 255 bytes, valid text without control
  characters. User names are checked as user names, team names as text without control characters; a list holds at
  most 100 entries.

## Reading

- `tag-protections.list` has no paging in either API. The answer is one list of at most 100 rules, and `truncated`
  is `true` when the instance sent more.
- `tag-protections.list` and `tag-protections.get` return every documented member: `id`, `name_pattern`,
  `whitelist_usernames`, `whitelist_teams`, `created_at`, and `updated_at`.

## Changes

- `tag-protections.create` needs `name_pattern`; the allowlists are optional. A rule without an allowlist leaves the
  matching tags to the instance's own rules.
- `tag-protections.update` sends only the members given, at least one. A list replaces the whole list, and an empty
  list clears it. A changed pattern can lower the protection of tags.
- `tag-protections.delete` removes a rule for good; the tags it covered stay and lose its protection. It is offered
  only to a connection whose `tools` list names it and is in no setup profile.
- Every change needs confirmation and sends exactly one request, never repeated, also not after a server error or a
  timeout; the message then says that the change may have been applied, and the rule must be read before it is tried
  again. `create` is not idempotent; `update` and `delete` are.

## Differences

Forgejo 15 through 17 and Gitea 1.27 and 28 document the same rule object, the same options, and the same routes;
no member belongs to one product only. An argument that no tool of the group offers is refused as `invalid-request`.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `tag-protections.list`, `tag-protections.get` | read | Gitea, Forgejo |
| `tag-protections.create` | create | Gitea, Forgejo |
| `tag-protections.update` | update | Gitea, Forgejo |
| `tag-protections.delete` | delete, listed-only | Gitea, Forgejo |
