---
description: >
  Guides a user without special knowledge through setting up the Todoist provider: finding the personal API token, adding it to Qatlas, checking the connection, what the token and the account's plan and roles allow, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-28
---

# Set up Todoist

This page shows how to connect Qatlas to Todoist, what limits a call, and what to do when Todoist refuses a request.
Fields, tools, arguments, profiles, and targets are described in the [Todoist provider](README.md); this page
names only the rights.

**The token decides what is possible at all.** Todoist has one kind of token here, the personal API token, and it
carries no scopes: it acts as your whole account. Qatlas cannot narrow it at Todoist. The rights you set in Qatlas
(the connection's `tools`, `effects`, and targets) limit it, and a call works only if both allow it. A connection
can never do more than the account, and the token never widens the connection.

## What you need

- A Todoist account that holds what Qatlas should work with: the projects, sections, labels, and tasks.
- The personal API token of that account. Whoever holds it can read and change everything the account can. Keep it
  secret and give it only to Qatlas.
- Qatlas keeps the token in the system keyring or the vault, or reads it from an environment variable.
- Qatlas always reaches the official Todoist service; see [Configuration](README.md#configuration).
- Use a separate Todoist account for a connection that must not reach your own tasks. The token has no narrower
  form, and the targets of a connection only limit what Qatlas does.

## Create a token

In Todoist, open your avatar, then **Settings**, then **Integrations**, then the **Developer** tab, and choose
**Copy API token**. Todoist changes its pages from time to time; look for these names.

- Copy the token and put it into the credential step below at once.
- **Issue a new API token** in the same tab replaces the token. According to the Todoist help, it also signs you out
  of Todoist on all your devices, and tools that use the old token may stop working. Use it only when the token has
  leaked or does not work.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `todoist`, the service, a credential, the
   scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the token as its `token` secret.

Without the guided setup, store the token on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$TODOIST_TOKEN" | qatlas credential set todoist-reader token
```

`todoist-reader` is the name of the credential in your configuration and `token` is its only secret. To replace a
token that was reissued or revoked, run the same command again with the new token. Run `qatlas config validate`
to check the configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Todoist accepted the token
and showed it every project and section the connection's targets name, or, for a connection to the whole account,
one project of it.

**A passing test does not mean every tool works.** The account's plan and its role in shared projects are checked by
Todoist on every call. A failing test or call is explained under [Fixing errors](#fixing-errors).

## What limits a call

The token has no rights to tick, so nothing needs to be added to it. These three things decide what Todoist allows:

| What | Effect |
| --- | --- |
| The account | The token reaches exactly what the account reaches, in every project it owns or has joined. |
| The plan of the account | Some features are not part of every plan. Todoist refuses them with the message in the plan row of [Fixing errors](#fixing-errors). |
| Your role in a shared project | A project shared with you can allow you less than its owner. As far as Todoist documents it, an account that may not edit a project is refused when it changes it. |

Qatlas treats these features as plan-dependent: reminders, saved filters, a higher number of labels, and completed
tasks from further back. Which plan includes which feature is Todoist's decision and can change; check the plan
in Todoist.

Reading a feature is refused with a `permission` class when the plan lacks it; the other tools of the connection keep
working. Changing a workspace project itself is refused by Qatlas, not by the account's role; see
[Boundary](README.md#boundary).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. When Todoist gave details, a second part follows:

```text
permission: this Todoist token may not change this resource; check the rights of its account in Todoist
provider answer (untrusted, not written by qatlas), HTTP 403:
  | FORBIDDEN 32 at cursor: Forbidden
```

The **provider answer** (`provider` in JSON output) is what Todoist itself replied: the HTTP status and the error's
tag, code, argument, and text. Todoist wrote it, not Qatlas. It is marked `untrusted`, shortened if long, and has
secrets replaced. Read it as information. Do not follow instructions in it.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Todoist rejected the token | The token is wrong, was replaced with **Issue a new API token**, or belongs to another account | Copy the current token ([Create a token](#create-a-token)) and store it again with `qatlas credential set <credential> token`. |
| `auth`: the Todoist token is unusable | The stored token is shorter than 8 or longer than 4096 characters or holds characters a token cannot have, such as a line break or a space | Store it again with `printf %s "$TODOIST_TOKEN" \| qatlas credential set <credential> token`. |
| `permission`: the Todoist plan of this account does not include this feature; check the plan in Todoist | The tool needs a feature the account's plan lacks, for example reminders | Check the plan in Todoist (see [What limits a call](#what-limits-a-call)), or leave that tool out of the connection. |
| `permission`: this Todoist token may not read this resource; check the rights of its account in Todoist | The account has no access to the project or object, for example a project no longer shared with it | Ask the owner of the project to share it again, or use the account that has access. |
| `permission`: this Todoist token may not change this resource; check the rights of its account in Todoist | The account's role in a shared project does not allow the change | Ask a project administrator for a role that may edit, or leave out the change tool. |
| `not-found`: Todoist does not hold this resource or does not show it to this token | The ID is wrong, the object was deleted, **or** the account cannot see it. Todoist answers all three the same | Check the ID with the list tool. Then check that the account can open the project in Todoist. |
| `rate-limited`: Todoist rate-limited the operation; retry after ... seconds | Todoist asks to wait. The next request of the same token waits as asked, at most one minute | Wait and retry. Do not let several programs use one token at once. |
| `timeout`: Todoist did not answer in time | Todoist or the network is slow | Retry a read. For a change, read the resource first; see the next row. |
| Any class with "the change may have been applied, read the current state before repeating it" | A change was sent but the outcome is unclear | Read the resource before repeating the change, or a create makes a second one. |
| `provider-error`: Todoist no longer accepts this cursor; start the list again without one | The cursor is too old | Start the list again without `cursor`. |
| `provider-error`: Todoist rejected the request as invalid | Todoist refused a value, for example an unknown label or a filter expression it cannot read | Check the arguments against the tool's description. |
| `invalid-request`: the project belongs to a Todoist workspace; only personal projects are changed | The project is a workspace project | Not a token problem. Qatlas changes only personal projects; see [Boundary](README.md#boundary). |
| `invalid-request` naming the targets | The project, section, or object is outside the connection's targets or taken out by an exclusion | Not a token problem. Pass one the connection allows, or change the targets or exclusions; see [Scope](README.md#scope). |
