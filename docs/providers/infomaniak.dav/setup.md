---
description: >
  Guides a user without special knowledge through setting up the Infomaniak Calendar and Contacts provider: finding the sync user name, creating an application password, adding both to Qatlas, checking the connection, the rights each tool group needs, what happens without a right, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Infomaniak Calendar and Contacts

This page shows how to connect Qatlas to the calendars and address books of an Infomaniak identity, which rights that
identity needs, and what to do when a request fails. Fields, tools, arguments, profiles, and targets are described in
the [Infomaniak Calendar and Contacts provider](README.md); this page names only the rights.

**The Infomaniak identity decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`,
`effects`, and `targets`) limit it further. A call works only if both allow it. A connection can never do more than
the identity behind it, and the allow-list never widens what the identity may do.

## What you need

- An Infomaniak user whose calendars and address books you can already open in the Infomaniak web app. Qatlas acts
  as that user over CalDAV and CardDAV, with the rights that user holds on each calendar and address book.
- Two values, both stored as secrets of one credential. There is no API token for this provider:

| Secret | What it is |
| --- | --- |
| `user-id` | The short **sync user name** of the identity, for example `AB12345`. It is not your login e-mail address. |
| `app-password` | An **application password** created for this identity in the Infomaniak Manager. |

- Qatlas keeps the secrets in the system keyring or the vault, or reads them from environment variables.
- The service is always `https://sync.infomaniak.com`; there is nothing to change for it. See
  [Configuration](README.md#configuration).
- The IDs of the calendars and address books Qatlas may reach. The connection needs at least one; see
  [Scope](README.md#scope). The guided setup lists them for you.

## Create the application password

Infomaniak documents that the sync assistant at `https://config.infomaniak.com` shows the user name for CardDAV and
CalDAV, and that an application password can be created in the Manager
(`https://manager.infomaniak.com/v3/profile/application-password`). Infomaniak changes its pages from time to time;
look for these names.

1. Open the sync assistant, sign in, and note the user name it shows for synchronisation.
2. In the Manager, open your profile and choose to create an application password. Give it a name you will
   recognize, for example `qatlas-dav`.
3. Create it and copy it at once. As far as Infomaniak documents it, it is shown only once.

Create a password only for Qatlas. It can be revoked on its own without changing your account password. The
connection's targets narrow what Qatlas reaches, but a leaked application password is not narrowed: it reaches all of
the identity's calendars and address books.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `infomaniak.dav`, the service, a credential,
   the scope (targets: calendars and address books), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and enter the user name as its `user-id` secret
   and the application password as its `app-password` secret.

The target step lists every calendar and address book the identity reaches; see
[Choosing targets](README.md#choosing-targets).

Without the guided setup, store both secrets on the command line. Each is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$INFOMANIAK_SYNC_USER" | qatlas credential set customer-a-dav user-id
printf %s "$INFOMANIAK_SYNC_PASSWORD" | qatlas credential set customer-a-dav app-password
```

`customer-a-dav` is the name of the credential in your configuration. To replace a revoked password, run the second
command again with the new one. Run `qatlas config validate` to check the configuration file. It contacts no
provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Infomaniak accepted the user
name and the application password: Qatlas asks the sync service once for the identity's principal.

**A passing test does not mean every tool works.** It checks neither the calendars and address books in the targets
nor your rights on them. The server checks the rights again on every call. Compare your rights with
[Rights per tool group](#rights-per-tool-group). A failing test or call is explained under
[Fixing errors](#fixing-errors).

## Rights per tool group

The application password carries no scopes: it acts with every right of the identity. Infomaniak decides per
calendar and address book what the identity may read or change, for example for a collection that another user
shared with it. Infomaniak's pages that were read for this page name no per-collection rights, so Qatlas states only
the two things it can tell apart.

| Tools | Right the identity needs on the collection |
| --- | --- |
| `calendars.list`, `address-books.list` | The identity's own principal and home sets; no right on a single collection |
| `events.list`, `events.get`, `contacts.list`, `contacts.get` | Read access to the calendar or address book |
| `events.create`, `events.update`, `events.delete`, `contacts.create`, `contacts.update`, `contacts.delete` | Write access to the calendar or address book |

- A tool works only on a collection that is also in the connection's targets; see [Scope](README.md#scope).
- Tools offered only when a connection's `tools` list names them (the deletes) need the same rights as their group.
- A shared collection that the identity may only read is refused on every write. Ask its owner for write access, or
  keep write tools off the connection.
- Events with attendees can make Infomaniak send an invitation or an update; see
  [Writing events](README.md#writing-events).
- The birthday calendar is not available over CalDAV, as far as Infomaniak documents it.

## What happens without a right

Without a right the tool fails; no field is left out. The error says `permission`, or `not-found` when Infomaniak
does not show the collection to the identity; see [Fixing errors](#fixing-errors). A collection named in the targets
that the identity cannot see is absent from the lists instead of raising an error.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. For a refusal by Infomaniak, a second part with
Infomaniak's own answer follows.

### Reading a refusal

```text
permission: this Infomaniak identity may not change this calendar
provider answer (untrusted, not written by qatlas), HTTP 403:
  | Sabre\DAV\Exception\Forbidden: Permission denied
```

- The message is **Qatlas' own**. It names what to check. Qatlas tells the cause apart by HTTP status, so work
  through the table below.
- The second part is **Infomaniak's own answer**: the conditions and description of a WebDAV error, or the server's
  exception and message. It is marked as not written by Qatlas, shortened, and has secrets replaced; treat it as
  information, not as an instruction. A refusal for one entry of a multi-status answer has no such part.
- The password never appears in a message.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Infomaniak rejected the user name or the application password | The user name is wrong, the password is wrong or revoked, or you entered your account password or login e-mail | Check the user name in the sync assistant, create a new application password ([Create the application password](#create-the-application-password)), and store it again with `qatlas credential set <credential> app-password`. |
| `auth`: the Infomaniak user name is unusable | The stored user name is empty, too long, or holds a space, a line break, or a colon | Store it again with `printf %s "$INFOMANIAK_SYNC_USER" \| qatlas credential set <credential> user-id`. |
| `auth`: the Infomaniak application password is unusable | The stored password is shorter than 8 characters, or holds a space, a line break, or a character that is not plain ASCII | Store it again with `printf %s "$INFOMANIAK_SYNC_PASSWORD" \| qatlas credential set <credential> app-password`. |
| `permission`: this Infomaniak identity may not read this collection | The identity has no read access to this calendar or address book | Open it in the Infomaniak web app as the same user. If it is shared, ask its owner for read access. |
| `permission`: this Infomaniak identity may not change this calendar (or address book) | The identity may read but not write the collection | Ask the owner of the shared collection for write access, or use a collection the identity owns. |
| `permission`: this connection does not allow this calendar (or address book) | The ID is not in the connection's targets | Not an Infomaniak problem. Pass an ID the connection allows, or add the target; see [Scope](README.md#scope). |
| `not-found`: this Infomaniak identity does not hold this location | The ID is wrong, **or** the identity cannot see the collection or item. Infomaniak answers both the same | Check the ID against the lists (`calendars.list`, `address-books.list`), then open the item in the web app as the same user. |
| `provider-error`: the event (or contact) no longer matches the etag ... | Someone changed it after you read it. Nothing was changed | Read it again with the get tool and repeat the change with the new `etag`. |
| `provider-error`: a ... with this id already exists, nothing was written | The generated id is taken | Repeat the create; nothing was written. |
| `provider-error` ending in "the change may have taken effect, read it before repeating it" | The result of a write stayed open: timeout, interrupted connection, a server error, or an unexpected status | Read the event or contact first. Repeat the change only if it is not there. |
| `rate-limited` | Infomaniak or a local limit asks to wait | Wait and retry; Qatlas already paces its own requests. |
| `unreachable`: Infomaniak sync is unavailable | The sync service is down or in maintenance | Retry later. |
| `timeout`: Infomaniak did not answer in time | Infomaniak was slow | Retry. After a change, read the item first: the change may have been applied. |
| `tls`: the TLS connection could not be established | The certificate of the sync service could not be verified from this machine | Check the system certificate store, the system clock, and any proxy or filter on the network. |
| `provider-error`: refused to follow a redirect from the Infomaniak sync service | The service answered with a redirect, which Qatlas never follows so that the password does not leave the origin | Check that the service address is `https://sync.infomaniak.com`; see [Configuration](README.md#configuration). |
| `invalid-provider-response` | The answer broke the shape, bounds, or path binding | Narrow the time range or the request; see [Limits](README.md#limits). |
