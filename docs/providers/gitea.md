---
description: >
  Describes the Gitea provider and what Gitea and Forgejo share: the access token and the base URL, token scopes per
  tool group, the users, organizations, and repositories as targets, the instance-wide tools, the version check and
  the connection test, pagination, errors and limits, the bounds of results, the excluded operations, and the setup
  profile.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Gitea

This provider reads the account behind an access token, the repositories of a self-hosted Gitea instance, and the
instance itself: its settings, version, signing key, and server-side rendering of Markdown. It also reads and
changes the issues of the bound repositories. A connection offers reads by default; a change needs the matching
effect on the connection and a confirmation.

Forgejo runs on the same code and the same rules. Everything below holds for both providers unless a line says
otherwise; [Forgejo](forgejo.md) lists only what differs. Where this page says "the instance", it means the Gitea or
the Forgejo instance of the connection.

## Configuration

`base_url` is required and has no default: `https://HOST`, optionally with a port, without a path, user info, query,
or fragment. An instance served below a path prefix is not supported. `qatlas config validate` refuses a missing or
other address with `config-invalid` without quoting it. Qatlas appends `/api/v1` and follows no redirect, so the token
never travels to another host.

The credential provides `token`, an access token created in the account's applications settings. It is sent as
`Authorization: token ...` and registered with the redactor. Nothing else authenticates a request: no basic
authentication, no token in the query, no one-time password header, and never the Sudo header or parameter; no
tool takes such an argument.

```yaml
services:
  forge:
    provider: gitea
    base_url: https://git.example.com

credentials:
  forge-token:
    provider: gitea
    type: keyring

connections:
  forge-acme:
    service: forge
    credential: forge-token
    targets:
      - orgs/acme
```

### Token scopes

Gitea and Forgejo scope a token by category, `read:` or `write:`: `activitypub`, `admin`, `issue`, `misc`,
`notification`, `organization`, `package`, `repository`, and `user`. A read needs the `read:` scope of its category,
a change the `write:` scope. The scope of a tool group follows from the route group the API serves it in; none was checked against a live instance, and a missing
scope shows as `not-found` (see Errors).

| Tools | Scope |
| --- | --- |
| `accounts.me` | `read:user` |
| `repositories.get` | `read:repository` |
| `repositories.list` | `read:repository`; the token's own repositories and a user's also `read:user`, an organization's also `read:organization` |
| `settings.get`, `signing-keys.get`, `markdown.render`, `markup.render`, `nodeinfo.get` | presumably `read:misc` |
| `version.get` | none |
| `issues.list`, `issues.get`, `issue-timeline.list` | `read:issue` |
| `issues.search` | presumably `read:issue`, and possibly `read:repository` |
| `issues.create`, `issues.update`, `issues.close`, `issues.reopen`, `issues.delete`, `issue-deadlines.set` | `write:issue`; a change also reads the issue first, which needs `read:issue` |
| target selection | `read:user` for the account, `read:organization` for its organizations |

A token with these scopes is the upper bound; the targets narrow a connection further, never the token.

## Targets

`targets` is an optional allow-list. Without it a connection reaches whatever its token reaches.

| Entry | Allows |
| --- | --- |
| `users/NAME`, `orgs/NAME` | every repository and everything else the owner holds |
| `repos/OWNER/REPO` | one repository |

Entries can be mixed; the allow-list is their union. Users and organizations share one namespace, so `users/acme`
and `orgs/acme` name the same owner and together make the file invalid. Names compare without case. A repository
that is renamed or moved answers under its old name too; Qatlas therefore checks the canonical `full_name` or
owner of every answer, not the spelling the caller used, and refuses what lies outside.

- `repository` takes `OWNER/REPO`; it may be left out only when the targets name exactly one repository. `owner`
  takes `users/NAME` or `orgs/NAME` and is never filled in from the targets. The Git remote of the working directory
  never chooses a target.
- A target argument outside the allow-list is refused before the token is read and before any request, and the
  refusal does not name it.
- `repositories.get` by numeric ID reads the repository once and returns it only when its canonical name lies inside
  the targets. `repositories.list` checks every row; rows outside the targets are dropped, a server-side filter is
  only an optimization.
- `accounts.me` is not offered by a connection whose targets name a repository: such a connection is scoped to it,
  and the account may belong to another customer.
- Target selection lists the owners from `GET /user` and `GET /user/orgs`, and the repositories from
  `GET /user/repos` at the top and `GET /users/NAME/repos` or `GET /orgs/NAME/repos` below an owner. A browser URL
  resolves in the forms `https://HOST/OWNER/REPO`, `.../issues/N`, `.../pulls/N`, and `https://HOST/NAME`; they are
  derived from the `html_url` of API answers and not confirmed by the products' documentation. A first path
  segment the instance reserves is not resolved.

## Instance-wide tools

`settings.get` (argument `section`: `api`, `repository`, `attachment`, or `ui`), `version.get`, `signing-keys.get`,
and `nodeinfo.get` (Forgejo only) read data of the instance, not of an owner or repository. Every connection offers
them, whatever its targets.

`markdown.render` and `markup.render` send a text to the instance and return HTML. They change nothing and carry
the `read` effect. Without `repository` the text renders with no repository, which touches no customer data. With
`repository` (`OWNER/REPO`, checked against the targets like any repository argument) the instance resolves links
and references against that repository. The request body holds only the documented members, built from validated
arguments: the text, the mode, the repository reference, and the members a product documents. `markdown.render`
with `raw` posts the text alone to the raw endpoint. The form of the repository reference differs by product and
was not tried against a live instance; an instance that rejects it answers `provider-error`.

`signing-keys.get` returns the public key the instance signs commits with, not an access credential. The format is
`gpg` for both products; the SSH public key is `pub` on Gitea and `ssh` on Forgejo. The other product's format is
refused before the token is read.

`max_response_items` and `default_paging_num` from `settings.get` describe the instance; paging still follows
Pagination below.

## Version and connection test

A connection accepts one product in a supported version: Forgejo from 15 (LTS), Gitea from 1.27 including the
version numbers from 28. Development builds count as their number. `qatlas connection test` first proves the
product and version fail-closed, then reads with the token:

1. `GET /api/forgejo/v1/version` without the token. A version means Forgejo.
2. Otherwise `GET /api/v1/version`, with the token only after the instance refused anonymous access. A version with
   the `+gitea-` suffix is Forgejo's compatibility marker, not a sign of Gitea.
3. A product other than the configured one, or a version below the minimum, fails as `provider-error` without
   quoting the instance. A Forgejo instance that blocks `/api/forgejo` by proxy is taken for Gitea and therefore
   refused for a `forgejo` connection.
4. Then the token is proven: `GET /user`, or, with targets, the first target.

A tool that needs a newer version than the product minimum reports itself unavailable before its request and names
the version it needs. The version is held in the process for a short time per instance and product and read only
for such tools. A passing test proves the product, the version, and one read; scopes and targets are decided per
call.

## Pagination

Lists take `limit` (1 to 50, 30 when left out) and `cursor`. The instance serves at most its own
`max_response_items` and silently returns fewer when `limit` is larger. The end of a list comes from the `Link` header,
or without it from `X-Total-Count`, never from a batch shorter than its limit. The cursor carries the page and the
batch size; a continuation with another `limit` is refused. Other endpoints of the API deviate, and a tool that
reads them follows its own form: Git trees take `per_page` (at most 1000), wiki revisions take `page` without
`limit`, `GET /repos/search` answers a `{"ok":true,"data":[...]}` wrapper, Actions lists can carry their own
wrappers, and commit lists add `X-HasMore`, `X-Page`, `X-PageCount`, and `X-PerPage`.

## Errors and limits

| Class | Answer |
| --- | --- |
| `auth` | HTTP 401: the token is rejected or expired |
| `not-found` | HTTP 403 or 404, with one message for both: the resource may not exist, or the token's scopes, its repository binding, or its public-only restriction do not reach it. Neither proves that a resource exists. |
| `rate-limited` | HTTP 429; the wait of `Retry-After` holds the next request, at most one minute |
| `unreachable` | HTTP 503 or a connection that fails |
| `provider-error` | a redirect (not followed), another status, a product or version mismatch, or a refused render |
| `invalid-provider-response` | an answer that is not readable within the bounds |

A change is sent once and never repeated. When its outcome is open (a timeout, a reset connection, a 5xx status, or
an answer that cannot be read), the message says that the change may have been applied and that the current state
has to be read before repeating it. Error bodies of the instance are untrusted data and never reach a message. Neither product documents an API
limit, but public instances limit by policy; Codeberg was seen announcing 2000 requests per 600 seconds. Qatlas
spaces the requests of one token by half a second and the anonymous probes per instance.

## Bounds

Every value of an answer is untrusted data. Responses are read up to 4 MiB, strings are cut at 2048 bytes, lists
inside an object at 100 entries, and free-form metadata at four levels. The body of an issue and the text of a
timeline event are cut at 64 KiB. A rendered text is accepted up to 256 KiB
and returns at most 1 MiB of HTML, with `truncated` set when it was cut; the HTML is never executed or followed.

Tools return every documented member of their endpoint, except:

- the owner inside a repository leaves out `last_login`, `login_name`, `source_id`, and `prohibit_login`; `accounts.me`
  shows them for the own account;
- `parent` keeps only `id` and `full_name`; the `doer` and `recipient` of `repo_transfer` keep `id` and `login`, its
  `teams` keep `id` and `name`;
- `original_url`, and any address with user info or a query, is left out because it may hold a credential;
- members the product's documentation marks as deprecated, such as the `Wiki` flag of Gitea's render requests.

Members only one product documents appear only there; the descriptor of each tool lists them.

## Tools

| Tool | Effect | Provider |
| --- | --- | --- |
| `accounts.me` | read | both |
| `repositories.list`, `repositories.get` | read | both |
| `settings.get`, `version.get`, `signing-keys.get` | read | both |
| `markdown.render`, `markup.render` | read | both |
| `nodeinfo.get` | read | Forgejo |
| `issues.list`, `issues.search`, `issues.get`, `issue-timeline.list` | read | both |
| `issues.create` | create | both |
| `issues.update`, `issues.close`, `issues.reopen`, `issue-deadlines.set` | update | both |
| `issues.delete` | delete, listed only | both |

Tool IDs carry the provider as prefix, such as `gitea.repositories.get`.

## Issues

Every issue tool takes `repository` (`OWNER/REPO`, checked against the targets like any repository argument) and,
for one issue, its `number`. Issues and pull requests share one index space.

- `issues.get`, `issues.update`, `issues.close`, `issues.reopen`, `issues.delete`, and `issue-deadlines.set` first
  read the issue. A pull request is refused as `invalid-request` with a pointer to the pull-request tools, before
  anything is changed, and so is an answer whose canonical repository lies outside the targets. `issues.list` and
  `issues.search` ask for issues only. `issue-timeline.list` also reads the timeline of a pull request.
- `issues.search` reads across repositories; every row is checked against the targets by the repository it reports,
  and a row without one is dropped, so a batch may hold fewer issues than the instance sent. `owner` must lie inside
  the targets. The server-side filters are only an optimization.
- Assignees are the `assignees` member of `issues.create` and `issues.update`; on an update the list replaces the
  current assignees and an empty list removes them. The user objects of authors and assignees are personal data.
- `issues.update` never changes the state and not the due date; `issues.close` and `issues.reopen` set the state,
  `issue-deadlines.set` sets the due date or, with an empty `due_date`, removes it. An issue that already has the
  requested state is returned with `changed` false and nothing is sent.
- `issues.delete` removes the issue and its comments for good. It is offered only by a connection whose `tools` list
  names it and is in no setup profile.
- The timeline shows another issue, pull request, or comment an event refers to by identifiers only, because it may
  lie outside the targets.
- Not offered: the deprecated `assignee` of create and update, and the due-date members of both, which
  `issue-deadlines.set` covers. A label is set on creation only, by its numeric ID; the update API has no label member.

## Not offered

No tool of either provider covers ActivityPub and federation, repository migration, creating mirrors, creating
tokens, keys, webhooks, or OAuth2 applications, creating users, adding email addresses, registering runners
(including registration tokens), or acting as another user (Sudo).

## Setup profile

The profile `read`, recommended, selects `accounts.me`, `repositories.list`, `repositories.get`, `settings.get`,
and `version.get`.

## Public instances

Codeberg (`https://codeberg.org`) and other public instances are ordinary instances here. Their operators may
restrict agent use and traffic; check their terms before pointing a connection at one.

## Gitea

- Gitea 1.27 and the numbering from 28 serve the same fields for the tools here.
- Tokens cannot be bound to repositories, so the targets are the only narrowing besides the scopes.
- `settings.get` with `api` also carries `default_max_response_size`.
- The user object carries `type`. The SSH signing key is `signing-keys.get` with `format` `pub`.
- The render modes are `markdown`, `comment`, `wiki`, and `file`, and the repository reference is the URL path
  `/OWNER/REPO`. The `Wiki` flag is deprecated and not offered; `mode` `wiki` replaces it, and there is no
  `branch_path`.
- Gitea has no `nodeinfo.get`, no `order_by`, and no `sort` on issue lists and searches.
- An issue carries `content_version`, `time_estimate` (read only), and `projects`. `issues.update` takes
  `content_version`, the version read before, to refuse an update after the title or body changed, and `projects`,
  which replaces the projects; `issues.create` takes `projects`. `issues.search` takes `created_by`.
