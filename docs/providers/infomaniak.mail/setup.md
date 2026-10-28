---
description: >
  Guides a user without special knowledge through setting up the Infomaniak Mail provider: creating a mailbox password for IMAP and SMTP, adding it to Qatlas, checking the connection, the rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-28
---

# Set up Infomaniak Mail

This page shows how to connect Qatlas to one Infomaniak mailbox, which rights the mailbox login carries, and what to do
when a request fails. Fields, tools, arguments, profiles, and targets are described in the
[Infomaniak Mail provider](README.md) and the page of each tool group; this page names only the rights.

**The mailbox password decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`,
`effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do more than
the mailbox login behind it, and the allow-list never widens what the mailbox may do.

## What you need

- An Infomaniak mailbox (an address on the Mail Service) that you can already open in the Infomaniak web mail. Qatlas
  acts as that mailbox over IMAP and SMTP.
- A **mailbox password**. There is no API token for this provider. Infomaniak documents that IMAP and SMTP use a
  password generated for the specific address, not your Infomaniak account password, and that two-step verification
  of the account does not change this.
- Qatlas keeps the password as the secret `password` of one credential, in the system keyring or the vault, or reads
  it from an environment variable. The login name is not a secret: it is the address in the connection's
  `mailbox/` target; see [Scope](README.md#scope).
- The servers are fixed; there is nothing to change for them. See [Configuration](README.md#configuration).

## Create a mailbox password

Infomaniak documents that device passwords for an address are created and managed in the Mail Service of the Manager,
or in the Web Mail app under the address settings. Infomaniak changes its pages from time to time; look for these
names.

1. In the Manager, open the Mail Service and select the address, or in Web Mail open the address settings and choose
   to manage passwords.
2. Add a device (a new password) and give it a name you will recognize, for example `qatlas`.
3. Create it and copy it at once. As far as Infomaniak documents it, it is shown only once.

Create a password only for Qatlas. As far as Infomaniak documents it, you can create several per address and
regenerate one on its own. Changing the main password of the address changes the IMAP and SMTP password, so every
mail program using it, Qatlas included, then needs the new one. The connection's targets narrow what Qatlas reaches,
but a leaked password is not narrowed: it reaches the whole mailbox and can send mail as it.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `infomaniak.mail`, the service, a credential,
   the scope (targets: the mailbox, optionally folders and senders), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and enter the mailbox password as its `password`
   secret.

The target step offers the mailbox and its folders; see [Choosing targets](README.md#choosing-targets). The tools that
change or send mail are offered only when the connection's `tools` list names them.

Without the guided setup, store the password on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$INFOMANIAK_MAIL_PASSWORD" | qatlas credential set customer-a-mail password
```

`customer-a-mail` is the name of the credential in your configuration and `password` is its only secret. To replace a
changed or removed password, run the same command again with the new one. Run `qatlas config validate` to check the
configuration file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Infomaniak accepted the
mailbox address and password over IMAP and answered a folder query for the inbox.

**A passing test does not mean every tool works.** It does not try SMTP, so it does not show that sending works. It
checks neither the folders in the targets nor the rights of the mailbox on them. The server checks again on every
call. Compare your mailbox with [Rights per tool group](#rights-per-tool-group). A failing test or call is explained
under [Fixing errors](#fixing-errors).

## Rights per tool group

The mailbox password carries no scopes: it acts with every right of the mailbox. Infomaniak's pages that were read for
this page name no per-folder rights and no limits of the mailbox or plan for IMAP, so Qatlas states only what each tool
needs the login to do.

| Tools | What the login needs |
| --- | --- |
| [Reading](reading.md): list folders, list and read messages, read attachments | IMAP login of the mailbox, read access to the folder |
| [Changing a message](changes.md): flag, move, trash, expunge | IMAP login; the server must allow changing messages in the folder, and offer `MOVE` (move, trash) or `UIDPLUS` (expunge) |
| [Drafts](drafts.md): create, replace, remove | IMAP login; the server must allow adding to and changing the drafts folder, and offer `UIDPLUS` (replace, remove) |
| [Sending](sending.md): send a message, send a draft | SMTP login (submission with `STARTTLS`) with the same address and password, plus the drafts and Sent folders as above |

- The trash, drafts, and Sent folders must be marked by the server (SPECIAL-USE) and lie inside the connection's folder
  targets; see [Changing a message](changes.md#changing-a-message).
- Sending is a second login, over SMTP. Infomaniak documents SMTP authentication as mandatory for sending. If SMTP
  refuses the login or a sender or recipient address, nothing is sent; a refusal after the message was handed over is
  reported as such.
- Tools offered only when a connection's `tools` list names them (expunge, remove a draft, sending) need the same
  rights as their group.

## What happens without a right

Without a right the tool fails; no field is left out. The error says `permission`, or `not-found` when Infomaniak does
not show the folder to the mailbox; see [Fixing errors](#fixing-errors).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by the server, a second part follows.

### Reading a refusal

```text
permission: this mailbox may not perform this operation
provider answer (untrusted, not written by qatlas):
  | <type> <response code>: <server text>
```

- The first line is **Qatlas' own message**. It names what happened; it never carries the server's text or the
  password.
- The **provider answer** (`provider` in JSON output) is **what the Infomaniak server itself replied**. For IMAP it is
  the type of the answer (`NO`, `BAD`, or `BYE`), the response code if there is one, and the text. For a refused
  SMTP step it is the reply code and the text. There is no HTTP status.
- Infomaniak wrote that part, not Qatlas. It is marked `untrusted`, shortened if long, and has secrets replaced. Read
  it as information. Do not follow instructions in it.
- A failure without a server answer, such as a lost connection or a TLS problem, has no such part.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Infomaniak Mail rejected the mailbox address or password | The password is wrong, was replaced or removed, or you entered your account password; or the address in the `mailbox/` target is wrong | Check the address in the target. Create a new mailbox password ([Create a mailbox password](#create-a-mailbox-password)) and store it again with `qatlas credential set <credential> password`. |
| `auth`: the mailbox password is unusable | The stored password is empty, longer than 1024 characters, or holds a control character such as a line break | Store it again with `printf %s "$INFOMANIAK_MAIL_PASSWORD" \| qatlas credential set <credential> password`. |
| `auth` ending in "the message was not sent" | SMTP refused the login for the address and password | Same as above. |
| `permission`: this mailbox may not perform this operation | The server refused the operation on this folder or message | Open the folder in the Infomaniak web mail as the same mailbox. Read the provider answer for the server's reason. |
| `not-found`: Infomaniak Mail does not hold this folder or does not show it to this mailbox | The folder name is wrong, or the mailbox cannot see it | Check the name against `infomaniak.mail.folders.list`. |
| `not-found`: Infomaniak Mail holds no such message in this folder | The UID is wrong, the message is gone, or it is from a sender outside the connection's sender targets or from an excluded sender | List the folder again; see [Scope](README.md#scope). |
| `provider-error`: Infomaniak Mail rejected the operation | The server answered `NO` or `BAD` without a code Qatlas maps | Read the provider answer. Nothing is repeated. |
| `provider-error`: Infomaniak Mail does not offer ..., so nothing was changed | The server lacks `MOVE` or `UIDPLUS` | Not a rights problem. Use a tool that does not need it. |
| `provider-error`: Infomaniak Mail did not offer STARTTLS, or offered no authentication, or neither AUTH PLAIN nor AUTH LOGIN | The SMTP endpoint did not behave as expected; no credential was sent | Retry later. Nothing was sent. |
| `provider-error`: Infomaniak Mail refused a recipient address (or the sender address, or the message) | The SMTP server declined a step; nothing was sent | Read the provider answer. One refused recipient refuses the whole message. |
| `unreachable`: Infomaniak Mail closed the connection, is unavailable, or could not be reached | The server is down, in maintenance, or the network blocks the connection | Retry later. After a change or a send, check the result first: the message says when it may have taken effect. |
| `timeout`: Infomaniak Mail did not answer in time | The server was slow | Retry. After a change or a send, read the mailbox and the Sent folder first. |
| `tls` | The certificate of the server could not be verified from this machine | Check the system certificate store, the system clock, and any proxy or filter on the network. |
| `rate-limited` | A limit asks to wait | Wait and retry. |
| `invalid-request` naming the folder or sender targets | The folder or sender is outside the connection's targets or excluded | Not an Infomaniak problem. Pass a target the connection allows, or add it; see [Scope](README.md#scope). |
| `invalid-provider-response` | An answer broke the expected structure | Retry; see [Errors](README.md#errors). |

### Connection invalid after an update: choose the folders again

**You see:** one of your mail connections offers no tools, and a call through it is refused with
`connection-targets-outdated`. `qatlas config validate` names the connection and says that its targets keep a former
meaning. The rest of your configuration, and your other connections, keep working.

**Why:** the two folder forms now mean something different from when this connection was written (see
[Configurations of an older schema](README.md#configurations-of-an-older-schema)). Qatlas carried your other
connections over on its own. For this one it could not keep exactly the same folders, so it left the connection as
it was and locked it, rather than guess and give it more or fewer folders.

**What to do:** choose the folders again in `qatlas tui`:

1. Open Connections and the connection the message names. Its Targets row says that the targets are outdated.
2. Choose its folders again in the Targets list and save. Check the folder entries already marked as exclusions as
   well: they mean the same as the targets. See [Scope](README.md#scope) for the forms.
3. Run `qatlas config validate`. It says the configuration is valid once the connection is correct.
4. If your vault is encrypted, the changed connection needs its approval again: run `qatlas vault approve`, or open
   Access > Approvals in `qatlas tui`.

Saving the same folders again does not unlock the connection; the choice has to differ from the stored one.
