---
description: >
  Describes the branch protection tools of the Gitea and Forgejo providers: listing, reading, creating, changing,
  reordering, and deleting the protection rules of a repository; target binding, rule names, the members both
  products share and those only one knows, confirmation, and limits.
type: knowledge
edit: shared
created: 2026-11-07
updated: 2026-11-07
---

# Branch protections

These tools show and change the branch protection rules of one repository. A rule is named by a branch name pattern,
`rule_name`, such as `main` or `release/*`; it covers every branch the pattern matches. No tool here creates,
renames, or deletes a branch ([Branches](branches.md)), and tag protection is another tool group. Rule names,
patterns, and the user and team names in the allowlists are untrusted data. The tools need a token that is an
administrator of the repository.

## Binding

- `repository` is chosen against the connection's targets and exclusions before the token is resolved or a request
  is sent; it may only be left out when the targets name exactly one repository.
- `rule_name` is checked before anything is sent: not empty, at most 255 bytes, valid text without control
  characters, not `.` or `..`. It is sent as one escaped path segment, a slash as `%2F`; whether the instance routes
  a name with a slash is the instance's decision, and a refusal arrives as `not-found`. The rule is found by its
  exact name, never by a branch it covers.
- User names are checked as user names, team names and status checks as text without control characters. A list
  holds at most 100 entries, patterns at most 4096 bytes, `required_approvals` is 0 through 100.

## Reading

- `branch-protections.list` has no paging in either API. The answer is one list of at most 100 rules, and
  `truncated` is `true` when the instance sent more.
- `branch-protections.list` and `branch-protections.get` return every documented member of the rule object. The
  deprecated `branch_name` is left out; it repeats `rule_name`. Rule IDs are not part of the object.

## Changes

- `branch-protections.create` needs `rule_name`; every other member is optional, and a member that is left out takes
  the instance's default. A rule without members blocks direct pushes. An existing name is refused.
- `branch-protections.update` sends only the members given, at least one. A list replaces the whole list, and an
  empty list clears it. The name of a rule cannot be changed.
- `branch-protections.reorder` takes `ids`, the numeric rule IDs in the new order of priority. The API names the IDs
  but shows none, so they must come from outside these tools; the instance applies the list to the rules of this
  repository only. `priority` of `create` and `update` sets one rule by name instead.
- `branch-protections.delete` removes a rule for good; the branches it covered stay and lose its protection. It is
  offered only to a connection whose `tools` list names it and is in no setup profile.
- Every change needs confirmation, also one that lowers a protection or changes the order of rules, since the first
  matching rule applies. Each sends exactly one request, never repeated, also not after a server error or a timeout;
  the message then says that the change may have been applied, and the rule must be read before it is tried again.
  `create` is not idempotent; `update`, `reorder`, and `delete` are.

## Differences

- Only Forgejo knows `apply_to_admins`.
- Only Gitea knows `priority`, `enable_force_push`, `enable_force_push_allowlist`, the `force_push_allowlist_*`
  members, `enable_bypass_allowlist`, the `bypass_allowlist_*` members, and `block_admin_merge_override`; only Gitea
  has `reorder`. `block_on_codeowner_reviews` needs Gitea 28 and is refused as not available on an older instance
  before the request.
- A member the product does not know is refused as `invalid-request` before the token is resolved.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `branch-protections.list`, `branch-protections.get` | read | Gitea, Forgejo |
| `branch-protections.create` | create | Gitea, Forgejo |
| `branch-protections.update` | update | Gitea, Forgejo |
| `branch-protections.reorder` | update | Gitea |
| `branch-protections.delete` | delete, listed-only | Gitea, Forgejo |
