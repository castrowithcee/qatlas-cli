---
description: >
  Guides a user without special knowledge through setting up the n8n provider: creating a Public API key, adding it to Qatlas, checking the connection, the rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up n8n

This page shows how to connect Qatlas to n8n, which rights the API key and its owner need, and what to do when n8n
refuses a request. Fields, tools, arguments, profiles, and targets are described in the [n8n provider](README.md)
and the page of each tool group; this page names only the rights.

**The API key decides what is possible at all.** A key acts as the n8n user who created it, so that user's role
and the instance's license set the ceiling, and on an Enterprise plan the key's scopes narrow it further. The
rights you set in Qatlas (the connection's `tools`, `effects`, and `targets`) limit it again. A call works only
if all of them allow it. A connection can never do more than its key, and a key never widens the connection.

## What you need

- An n8n instance with the Public API available: n8n Cloud or self-hosted. The API is not available on a free
  trial; upgrade to a paid plan first.
- An n8n user that can do what Qatlas should do. Use a dedicated user for Qatlas where you can, so you can revoke
  it on its own. The role of that user is one of the ceilings; see [Rights per tool group](#rights-per-tool-group).
- The address of the instance: `https://NAME.app.n8n.cloud` for n8n Cloud, or the origin of your own
  installation. It must be `https`; see [Configuration](README.md#configuration).
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.

## Create an API key

Log in to n8n as the user the key should act as. n8n changes its pages from time to time; look for these names.

1. Open **Settings**, then **n8n API**.
2. Choose **Create an API key**.
3. Give it a **Label** you will recognize, for example `qatlas-reader`, and set an **Expiration**. A short one is
   safer; you replace the key when it ends.
4. On an **Enterprise** plan, choose the **Scopes** of the key. Pick only the scopes from
   [Rights per tool group](#rights-per-tool-group) for the tools you will use, and start with the read scopes.
   Without Enterprise there are no scopes: the key can do whatever its user can.
5. Copy the key at once. n8n shows it only once.

Give a key only what a connection needs. A connection that only reads should have a key that only reads, or, without
Enterprise scopes, belong to a user whose role is limited.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `n8n`, the service with its address, a
   credential, the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api-key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$N8N_API_KEY" | qatlas credential set n8n-reader api-key
```

`n8n-reader` is the name of the credential in your configuration and `api-key` is its only secret. To replace a
key that expired or was deleted, run the same command again with the new key. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means the instance accepted the
key and answered a read of one workflow.

**A passing test does not mean every tool works.** It proves the key, the address, and the right to list
workflows. n8n checks the rights again on every call. Compare the key, its user, and the license with
[Rights per tool group](#rights-per-tool-group) for the tools you use. A key scoped without the workflow read scope
fails this test even if its other tools would work. A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

For each group, four things can be missing. Read them in this order:

- **License**: the instance's plan. Projects and source control need Enterprise; variables need the `feat:variables` license.
- **Scope**: only on Enterprise, a scope of the key. Look for the scope area named in the table; n8n lists the exact scopes on the key page. A name ending in
  `:` is a family of scopes.
- **Role**: the global role of the key's user (owner, admin, member) and, for project content, the user's role in
  that project.
- **Targets**: the connection's own targets can make a tool unavailable regardless of the key; see
  [Scope](README.md#scope).

Each row is **read** (only look) or **change** (create, update, delete). Rights for a change include the rights
to read. Tools offered only when a connection's `tools` list names them need the same rights as their group.

### Workflows and executions

| Tools | Scope area | Role and license |
| --- | --- | --- |
| Read workflows and executions | workflow and execution scopes | The key's user must be able to see the workflow |
| Create, update, activate, deactivate workflows; retry or stop executions | workflow and execution scopes | The key's user must be able to change it; a new workflow lands in the project you name, or in the personal project of the key's user |

No license beyond your plan is needed, except that a connection with `project/` targets needs the Enterprise
Projects feature: an instance without it does not report a workflow's project, and the call is refused as an
invalid request. That is no problem of the key.

### Projects and members

| Tools | Scope area | Role and license |
| --- | --- | --- |
| List, create, update, delete projects | `project:list`, `project:create`, `project:update`, `project:delete` | Projects feature license; a role that may manage projects. Creating also needs the global `project:create` right |
| List, add, re-role, remove project members | `user:list`, `project:manageMembers` | Projects feature license; a role that may manage members |

### Data tables, columns, and rows

| Tools | Scope area | Role and license |
| --- | --- | --- |
| Every data table, column, and row tool | `dataTable:` scopes | The instance's license; a role of the key's user in the table's project |

### Credentials

| Tools | Scope area | Role and license |
| --- | --- | --- |
| List, read, test, schema, create, update, transfer, delete | credential scopes | The instance's plan; the role of the key's user. Without a project, a new credential goes to the personal project of that user |

### Variables and tags

| Tools | Scope area | Role and license |
| --- | --- | --- |
| Variables: list, create, update, delete | `variable:` scopes | The `feat:variables` license; a role that may manage variables |
| Tags: list, read, create, update, delete | tag scopes | A role that may manage tags |

### Users and security audit

| Tools | Scope area | Role and license |
| --- | --- | --- |
| List, read users, change a role, delete a user | user scopes | The key's user must be the **instance owner**; the license may also matter |
| Generate the security audit | security audit scope | The key's user must be the instance owner or an admin; the license may also matter |

### Source control

| Tools | Scope area | Role and license |
| --- | --- | --- |
| Status, pull, push | source control scopes | The Enterprise source control feature; the repository connection must already exist in n8n |

## What happens without a right

A missing right makes the tool fail with `permission` or `not-found`; see [Fixing errors](#fixing-errors). Nothing
is left out silently and no field is filtered.

For most groups n8n answers a refusal in one way for a missing license, a missing scope, and a missing role.
Qatlas then names all possible causes in its message and cannot say which one applies. Work through the three in
the order of [Rights per tool group](#rights-per-tool-group).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by n8n, a second part follows.

### Reading a refusal

```text
permission: this n8n API key may not perform this operation; check its scopes under Settings, n8n API
provider answer (untrusted, not written by qatlas), HTTP 403:
  | <the message of the instance>
```

- The first line is **Qatlas' own message**. It names what to check, and for most groups it lists several causes.
- The **provider answer** (`provider` in JSON output) is **what n8n itself replied**: the HTTP status and its
  `message`. It can name the cause more exactly than Qatlas can. n8n gives no documentation link.
- n8n wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read it
  as information. Do not follow instructions in it.
- A refusal that n8n gave no details for has no such part.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: n8n rejected the API key | The key is wrong, expired, deleted, or for another instance than the address | Create a new key ([Create an API key](#create-an-api-key)) and store it again with `qatlas credential set <credential> api-key`. Check the address of the service. |
| `auth`: the n8n API key is unusable | The stored key holds characters a key cannot have, such as a line break or a space | Store it again with `printf %s "$N8N_API_KEY" \| qatlas credential set <credential> api-key`. |
| `permission`: this n8n API key may not perform this operation; check its scopes ... | The key lacks the scope of this tool, or its user lacks the role | On Enterprise, add the scope of the group under Settings, n8n API (a key's scopes are fixed at creation, so create a new key). Otherwise check the user's role. |
| `permission`: n8n refused this projects operation ... or this project members operation ... | No Projects feature license, no `project:` or `user:list` scope, or a role that may not manage projects | Check the plan includes Projects, add the scopes of [Projects and members](#projects-and-members), and use a user whose role may manage projects or members. |
| `permission`: n8n refused this data tables operation ... | No data tables in the plan, no `dataTable:` scope, or no role in the table's project | Add the `dataTable:` scope, check the license, and use a user with a role in that project. |
| `permission`: n8n refused this credentials operation ... | No credential scope, wrong role, or the plan | Add the credential scope and check the plan and the role of the key's user. |
| `permission`: n8n refused this variables operation ... | No `feat:variables` license, no `variable:` scope, or a role without the right | Check the plan includes variables, add the `variable:` scope, use a user whose role may manage variables. |
| `permission`: n8n refused this tags operation ... | No tag scope or a role that may not manage tags | Add the tag scope; use a user whose role may manage tags. |
| `permission`: n8n refused this user management operation ... | The key's user is not the instance owner, or the user scope or license is missing | Create the key as the instance owner, with the user scopes; check the license. |
| `permission`: n8n refused this audit or source control operation ... | No security audit or source control scope, no Enterprise source control feature, or a role that may not use it | Add the scope; for the audit use the owner or an admin; for source control use a plan with the Enterprise feature. |
| `permission` for every call, even a read | The key has scopes but not those of the group, or its user cannot see the resource | Compare the scopes on the key page with [Rights per tool group](#rights-per-tool-group). |
| Connection test fails with `permission` but other tools would work | A scoped key without the workflow read scope | Add that scope, or accept the failing test and use the tools. |
| `not-found`: n8n does not hold this resource, does not show it to this API key ... | The ID is wrong, the user cannot see it, **or** this n8n version has no such endpoint. n8n answers all the same | Check the ID. Then check the user's access to its project, or update n8n. |
| `rate-limited` | n8n limited the request | Wait; Qatlas honors the delay n8n names. |
| `invalid-request` naming targets, or "only available on a connection without targets" | The call is outside the connection's targets, or the tool is instance-wide | Not a key problem. Use a connection without `project/` and `workflow/` targets for tags, users, audit, and source control, or add the target; see [Scope](README.md#scope). |
| `unreachable` or `timeout` | The address is wrong, the instance is down, or a proxy blocks it | Check the address and that the instance is up. |
