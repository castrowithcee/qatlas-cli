---
description: >
  Guides a user without special knowledge through setting up the Gitea provider: creating an access token, adding it to Qatlas, checking the connection, the token scopes each tool group needs, what happens without a scope, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Gitea

This page shows how to connect Qatlas to a Gitea instance, which scopes the token needs, and what to do when the
instance refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Gitea provider](README.md) and the page of each tool group; this page names only the token scopes.

**The token decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than its
token, and a token never widens the connection.

## What you need

- A Gitea account that can see what Qatlas should work with: the repositories and organizations. Use an account
  of its own for Qatlas when you can; then its reach is exactly what you gave that account.
- The address of your instance, as `https://HOST` with an optional port and no path; see
  [Configuration](README.md#configuration). The address belongs to the service, not to the token.
- A personal access token of that account. Gitea scopes a token by **category**, and each category has a `read:`
  and a `write:` scope. What it reaches is decided by its scopes and by the account.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Forgejo uses the same rules; see [Set up Forgejo](../forgejo/setup.md) for what differs.

## Create a token

In Gitea, open your account menu, then **Settings**, then **Applications**. Find the part that manages access
tokens and generate a new token. Gitea changes its pages from time to time; look for these names.

1. Give it a name you will recognize, for example `qatlas-reader`.
2. Set an expiry if the page offers one. A short one is safer; you replace the token when it ends.
3. Set the permission of each category from [Rights per tool group](#rights-per-tool-group) for the tools you
   will use: **Read**, or **Read and Write** to change. Start with the read permissions. Leave the other
   categories on no access.
4. If the page offers to limit the token to public resources, do not choose that for private data: such a token
   sees only public data, and some tools refuse it.
5. Generate the token and copy it at once. Gitea shows it only once.

Gitea requires at least one scope on every token, also for reading public repositories. Give a token only what a
connection needs. A connection that only reads should have a token that only reads.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `gitea`, the service (with its address), a
   credential, the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$GITEA_TOKEN" | qatlas credential set forge-token token
```

`forge-token` is the name of the credential in your configuration and `token` is its only secret. To replace a
token that expired or was revoked, run the same command again with the new token. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Gitea is a supported
version and accepted the token for the first target of the connection, or, with no targets, for your own account.
A repository target needs `read:repository` for this check; an account, an organization, or no target needs
`read:user`.

**A passing test does not mean every tool works.** Gitea checks the scopes again on every call. Compare the token
with [Rights per tool group](#rights-per-tool-group) for the tools you use. A failing test or call is explained
under [Fixing errors](#fixing-errors).

## Rights per tool group

Find your tool group, then give the token the scope. Each scope is **read** (`read:CATEGORY`, only look) or
**change** (`write:CATEGORY`, create, update, delete). `write:` includes `read:` of the same category. A change
of something by its ID first reads it, which the `write:` scope covers.

None of these scopes was checked against a live instance. They follow the route group of the Gitea API that
serves each tool, so treat them as likely, not certain. A missing scope shows as `not-found`; see
[Fixing errors](#fixing-errors).

### Account, users, and instance

| Tools | Scope |
| --- | --- |
| Read the own account ([accounts.me](README.md#tools)), users and their feeds, followers, following, the settings, emails, and blocks of the account ([Users](users.md)) | `read:user` |
| Follow or unfollow, change settings or the avatar, delete an email address, block or unblock | `write:user` |
| Starred and watched repositories of an account, starring ([Stars and watching](stars-and-watching.md)) | `read:user`; `write:user` to star |
| Stargazers and watchers of a repository, the watch state | `read:repository`; `write:repository` to watch |
| Version | none |
| Settings, signing key, Markdown and markup rendering ([Instance-wide tools](instance.md)) | `read:misc` |
| Choosing the account or an organization as a target | `read:user`; `read:organization` |

A token limited to public resources makes Gitea refuse the settings and email tools of the account and the
webhooks, keys, and OAuth2 applications of the account.

### Repositories, branches, tags, and contents

| Tools | Scope |
| --- | --- |
| List, read, search repositories ([Gitea provider](README.md#tools)) | `read:repository`; the token's own repositories and a user's also `read:user`, an organization's also `read:organization` |
| Create, change, fork, generate, sync, transfer, delete, avatars ([Repository lifecycle](repositories.md)) | `write:repository`; creating one in an organization also `write:organization` |
| [Branches](branches.md), [Tags](tags.md) | `read:repository`; `write:repository` to change |
| [Contents](contents.md): files, archives, writing files as commits | `read:repository`; `write:repository` to write |
| [Commits, comparison, status, and Git objects](commits.md) | `read:repository`; `write:repository` for commit statuses and Git notes |
| [Wiki](wiki.md) | `read:repository`; `write:repository` to change |
| [Releases](releases.md) and their assets, including the download | `read:repository`; `write:repository` to change |

### Issues and everything attached to them

| Tools | Scope |
| --- | --- |
| [Issues](issues.md): list, read, timeline | `read:issue` |
| Issues: create, change, close, reopen, delete, due dates | `write:issue` |
| Issues: search | `read:issue`, possibly also `read:repository` |
| Issue templates and the issue configuration | `read:repository` |
| [Comments](comments.md), [Reactions](reactions.md), [Issue dependencies](issue-dependencies.md), [Issue pins](issue-pins.md), [Issue subscriptions](issue-subscriptions.md), [Time tracking](time-tracking.md), [Milestones](milestones.md) | `read:issue`; `write:issue` to change |
| [Issue locks](issue-locks.md) | `write:issue` |
| [Assignees](assignees.md), [Attachments](attachments.md) | `read:issue` and `read:repository`; `write:issue` to change |
| [Labels](labels.md) of a repository | `read:issue`; `write:issue` to change |
| Labels of an organization | `read:organization`; `write:organization` to change |
| [Issue labels](issue-labels.md) | `read:issue` and `read:organization`; `write:issue` to change |
| [Projects](projects.md) (Gitea 28 and later) | `read:issue`, plus `read:repository` or `read:organization` for the projects of that owner; the matching `write:` scopes to change |

### Pull requests

| Tools | Scope |
| --- | --- |
| [Pull requests](pull-requests.md) and [reviews](pull-request-reviews.md): read | `read:repository` |
| Create, change, merge, review, request reviewers | `write:repository` |

### Organizations and teams

| Tools | Scope |
| --- | --- |
| [Organizations](organizations.md): list, read, members, blocked users | `read:organization`; the lists of the own organizations and the permissions of a user also accept `read:user` |
| Create, change, rename, delete, members, blocking | `write:organization` |
| [Teams](teams.md) | `read:organization`; `write:organization` to change |
| Teams without an organization (the own teams of the account) | `read:user` |
| Teams of a repository | `read:repository`; `write:repository` to change |

A token limited to public resources sees only public organizations.

### Actions

| Tools | Scope |
| --- | --- |
| [Actions](actions.md): runs, jobs, tasks, workflows, artifacts, logs | `read:repository` |
| Cancel, re-run, approve, dispatch, delete | `write:repository` |
| [Actions administration](actions-admin.md): secrets, variables, runners, workflow state | the category of the level: `repository` for a repository, `organization` for an organization, `user` for the user level; `read:` to read, `write:` to change |

### Packages and notifications

| Tools | Scope |
| --- | --- |
| [Packages](packages.md) | `read:package`; `write:package` to change |
| [Notifications](notifications.md) | `read:notification`; `write:notification` to change. Binding a thread or a repository also reads it, which needs the scope of that read. |

### Webhooks, keys, mirrors, and applications

The tools of [Accesses](accesses.md) need the `read:` scope of the category in the table, and the `write:` scope to
change or run something. The instance serves them only to the people named in the last column; anyone else gets
`not-found`.

| Tools | Category | The instance serves |
| --- | --- | --- |
| Webhooks of a repository | `repository` | repository administrators, while webhooks are enabled |
| Webhooks of an organization | `organization` | organization owners, while webhooks are enabled |
| Webhooks of the account | `user` | the account, while webhooks are enabled; no token limited to public resources |
| Git hooks | `repository` | repository administrators allowed to edit Git hooks, while Git hooks are enabled |
| Push mirrors, deploy keys | `repository` | repository administrators; push mirrors also need mirroring switched on |
| Keys, GPG keys, OAuth2 applications of the account | `user` | the account; no token limited to public resources |
| Keys and GPG keys of another user | `user` | any reader the instance lets see the user |
| The token the connection uses (`access-tokens.get-current`, `.delete-current`) | none | any valid token |

### Instance administration

| Tools | Scope |
| --- | --- |
| [Instance administration](administration.md): runners, workflow runs, workflow jobs, packages, user accounts, email addresses, and organizations of the whole instance, and organizations and repositories created for users | `read:admin`; `write:admin` to change |

The account of the token must be a **site administrator** of the instance. Use a token of its own for these tools
that no other connection uses.

## What happens without a scope

A call the token has no scope for fails. Gitea answers it with HTTP 403 or 404, and Qatlas reports both the same
way: `not-found`. The error cannot tell a scope that is missing from a name that is wrong; see
[Fixing errors](#fixing-errors). Qatlas never leaves out a field instead.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by the instance, a second part
follows.

### Reading a refusal

```text
not-found: Gitea does not show this resource to this token; it may not exist, or the token's scopes or repository binding do not reach it
provider answer (untrusted, not written by qatlas), HTTP 403:
  | <the message of the instance>
  | documentation: https://gitea.example.com/api/swagger
```

- The first line is **Qatlas' own message**. It does not say which scope is missing.
- The **provider answer** (`provider` in JSON output) is **what Gitea itself replied**: the HTTP status, its
  `message`, and the documentation link of the answer. It often names the missing scope more exactly than Qatlas
  can. After a redirect, only the status is shown.
- Gitea wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read
  it as information. Do not follow instructions in it.
- A refusal that Gitea gave no details for has no such part.
- The words "repository binding" in the message concern Forgejo tokens. A Gitea token cannot be bound to
  repositories; see [Gitea](README.md#gitea-1).

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Gitea rejected the token; check that it is valid and not expired | The token is wrong, expired, revoked, or from another instance than the service address | Create a new token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. Check the address of the service. |
| `auth`: the Gitea token is unusable | The stored token holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$GITEA_TOKEN" \| qatlas credential set <credential> token`. |
| `not-found`: Gitea does not show this resource to this token ... | The name is wrong, **or** the token cannot reach it. Gitea answers both the same | Check the name (`OWNER/REPO`, the owner, the number). Then check the scope of the tool group in [Rights per tool group](#rights-per-tool-group): `read:repository` for repositories, `read:issue` for issues, `read:organization` for organizations, `read:user` for users. Read the provider answer for the exact scope. |
| `not-found` on a change | The token can read but not write | Raise the permission of that category to **Read and Write** (`write:repository`, `write:issue`, `write:organization`, `write:user`, ...). |
| `not-found` for a private repository or organization | The token is limited to public resources, or the account cannot see it | Create a token without the public-only limit, and make sure the account is a member or collaborator. |
| `not-found` for webhooks, Git hooks, push mirrors, or deploy keys | The account is not an administrator of the repository or organization, or the feature is off | Use an account with that role, with the `repository` or `organization` scope of [Webhooks, keys, mirrors, and applications](#webhooks-keys-mirrors-and-applications); ask an administrator to enable the feature. |
| `not-found` for the tools of the instance administration | The account is no site administrator, or the token lacks `read:admin` / `write:admin` | Use a site administrator and add the scope. |
| `not-found` for a wiki | The wiki is disabled, or the page does not exist | Check the name; enable the wiki in the repository settings; `read:repository`. |
| `not-found`: Gitea does not show the first target of this connection to this token | The first target does not exist, or the token cannot reach it | Check the target ([Targets](README.md#targets)); `read:repository` for a repository, `read:user` for an account, `read:organization` for an organization. |
| `provider-error`: the instance does not identify as Gitea; check the base URL and the provider of the service | The address belongs to Forgejo or to something else, or a proxy hides the product | Correct the base URL or the provider of the service. |
| `provider-error`: the instance runs a Gitea version older than ... | The instance is older than the supported version | Update the instance; see [Version and connection test](README.md#version-and-connection-test). |
| `provider-error`: this operation is not available on this instance; it needs Gitea ... or later | The tool needs a newer Gitea | Update the instance, or do not use the tool. |
| `config-invalid` on `base_url` | The address is not `https://HOST` | Remove the path, user, query, or fragment from the address; see [Configuration](README.md#configuration). |
| `rate-limited` | The instance limits requests | Wait and retry. |
| `unreachable` | The instance is unavailable or in maintenance | Check the instance and the network. |
| `invalid-request` naming the targets | The repository or owner is outside the connection's targets | Not a token problem. Pass a target the connection allows, or add it; see [Targets](README.md#targets). |
