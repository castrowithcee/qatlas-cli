---
description: >
  Guides a user without special knowledge through setting up the Nextcloud provider: creating an app password, adding it to Qatlas, checking the connection, the rights, roles, and apps each tool group needs, what happens without them, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Nextcloud

This page shows how to connect Qatlas to Nextcloud, which rights and apps each tool group needs, and what to do when
Nextcloud refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Nextcloud provider](README.md) and the page of each tool group; this page names only the rights.

**The Nextcloud account decides what is possible at all.** An app password has no rights of its own: Qatlas acts
as the account that owns it, with that account's files, shares, group memberships, and roles. The rights you set in
Qatlas (the connection's `tools`, `effects`, and `targets`) limit it further. A call works only if both allow it. A
connection can never do more than its account, and an app password never widens the connection.

## What you need

- A Nextcloud account that can see what Qatlas should work with: files, boards, conversations, calendars, and so
  on. Use an account of its own for Qatlas when you can; then its reach is exactly what you gave that account.
- The address of the instance, which must use `https`; see [Configuration](README.md#configuration).
- The **user ID** of the account and an **app password** (a device password, not the login password). The user ID
  is the name Nextcloud uses for the account in the address of its files. It can differ from the display name and
  from the e-mail address you log in with.
- The apps of the tool groups you use. Files, shares, calendar, contacts, and the account data are part of
  Nextcloud itself; the rest are apps an administrator can switch off:

| Tool group | App that must be active |
| --- | --- |
| `deck` | Deck |
| `talk` | Talk (`spreed`) |
| `notes` | Notes |
| `account` | Notifications, Activity, and User status, each for its own tools |
| `files` | `files_trashbin` for the trash bin; it decides whether a delete is recoverable |
| `shares` | Federated sharing, for the remote shares only |

## Create an app password

In Nextcloud, open your **Personal settings**, then **Security**. At the bottom, under the list of connected
devices and sessions, enter a name you will recognize, for example `qatlas`, and create a new app password.
Nextcloud changes its pages from time to time; look for these names.

1. Copy the password at once. Nextcloud shows it only once.
2. Note your user ID.
3. To end Qatlas' access later, open the menu beside the device in the same list and revoke it. Only this password
   stops working; your login password and other devices stay.

Never use your login password instead: an app password can be revoked alone. Create one app password per Qatlas
credential, so you can revoke each on its own.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `nextcloud`, the service (the instance
   address), a credential, the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and enter the user ID as its `user-id` secret and
   the app password as its `app-password` secret.

Without the guided setup, store both values on the command line. Each is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$NEXTCLOUD_USER" | qatlas credential set nextcloud-reader user-id
printf %s "$NEXTCLOUD_APP_PASSWORD" | qatlas credential set nextcloud-reader app-password
```

`nextcloud-reader` is the name of the credential in your configuration. To replace a revoked app password, run the
second command again with the new one. Run `qatlas config validate` to check the configuration file. It contacts
no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Nextcloud accepted the user ID
and the app password and answered the reads named under [Configuration](README.md#configuration).

**A passing test does not mean every tool works.** The test checks the Files root or folder, the bound calendars, or
the administrator right, depending on the targets. It does not touch Deck, Talk, Notes, contacts, the account tools,
or any write. Compare the account with [Rights per tool group](#rights-per-tool-group) for the tools you use. A
failing test or call is explained under [Fixing errors](#fixing-errors).

## Rights per tool group

Nextcloud checks the rights again on every call. Each row names what the account needs in Nextcloud; the
connection's `tools`, `effects`, and `targets` decide which tools Qatlas offers at all.

### Files

| Tools | The account needs |
| --- | --- |
| List, stat, read, search, favorites | Read access to the path |
| Create, update, move, copy, create folder, delete, favorite | Write access to the path; enough storage quota |
| Trash bin: list, restore, delete | The `files_trashbin` app, and the deleted items of this account |
| Versions: list, get, restore | Read access to the file; restoring needs write access |
| File tags: list, add, remove | Read access to the file; the tag must be visible and assignable to the account |
| System tags: list | An `account` target |
| System tags: create, update, delete | The rights Nextcloud gives the account for tags; a tag hidden from users, or one that cannot be assigned, needs an administrator |
| File comments: list, create | Read access to the file |
| File comments: update, delete | Being the author of the comment; Nextcloud allows no other change |

- A file another user shared with the account has the rights of that share. A read-only share refuses writes.
- A file locked by an app or another user refuses changes until it is unlocked.
- Whether a deleted item can be restored depends on the `files_trashbin` app; Qatlas cannot tell beforehand.

### Shares

| Tools | The account needs |
| --- | --- |
| Shares: list, get | Being the owner of the item, or the recipient of an incoming share |
| Shares: create, update, delete | The right to share the item; an instance that allows sharing with that user or group. Changing or revoking needs a share the account made |
| Find recipients | Sharing is allowed for the account; the instance may limit which users and groups the account can pick |
| Incoming shares: list pending, accept, decline | An offer addressed to the account |
| Remote shares: list, get, remove | Federated sharing switched on in the instance, and a federated share the account accepted |

The administrator of the instance can forbid sharing, require an expiry date, or limit recipients. Qatlas cannot
override that.

### Deck

| Tools | The account needs |
| --- | --- |
| Boards, stacks, cards, comments, attachments: read | The Deck app, and a board shared with or owned by the account |
| Cards, comments, attachments: change | The right to edit on the board; Deck decides |
| Boards, stacks, labels, board sharing: change | The right to manage the board |
| Create a board | The Deck app |
| Comments: update, delete | Being the author of the comment |

### Talk

| Tools | The account needs |
| --- | --- |
| Conversations, participants, messages, polls: read | The Talk app, and a conversation the account takes part in |
| Post, react, vote, create a poll | A conversation that is not read-only; in a conversation with an active lobby, polls need a moderator |
| Edit, delete a message | Being the author of the message; Talk limits the age |
| Create a conversation | The Talk app; Talk may limit who can create conversations |
| Change or delete a conversation | Being the owner or a moderator of an ordinary, internal conversation |
| Add, remove participants, change moderators | Being the owner or a moderator |
| Close a poll | Being the author of the poll or a moderator |
| Share a file into a conversation | Being the owner of the item, or the right to share it, and a private one-to-one or group conversation |

### Calendar

| Tools | The account needs |
| --- | --- |
| Calendars, events, tasks, trash: read | Own calendars, or calendars shared with the account |
| Events, tasks: create, update, delete | A calendar that the account may write to; a calendar shared read-only refuses |
| Update, delete, or share a calendar | Being the owner of the calendar |
| Create a calendar | An account that may have calendars of its own; the connection needs the general `calendar` target |
| Trash: list, restore, delete | A bound calendar of the account; Nextcloud decides |

### Contacts

| Tools | The account needs |
| --- | --- |
| Address books and contacts: read | Own address books, or address books shared with the account |
| Contacts: create, update, delete | An address book that the account may write to; a read-only address book refuses |
| Update, delete, or share an address book | Being the owner of the address book |
| Create an address book | An account that may have address books of its own; the connection needs the general `addressbook` target |

The system address book is read-only whatever the account's rights.

### Notes

| Tools | The account needs |
| --- | --- |
| Notes and attachments: read, create, update, delete | The Notes app, and notes of this account; a read-only note refuses changes |

### Account

| Tools | The account needs |
| --- | --- |
| Account data | The account itself; the data of other accounts is never read |
| Notifications: list, dismiss | The Notifications app |
| Activity: list | The Activity app |
| Status: get, set, clear | The User status app |
| Search: providers, query | The unified search of the instance; only the providers the instance offers |

### Administration

| Tools | The account needs |
| --- | --- |
| Users and groups: list, read, members | An administrator, or a subadmin of the groups concerned; a subadmin sees only its own groups and their users |

Qatlas offers no change to users or groups.

## What happens without a right

The tool fails with `permission`, `not-found`, or `provider-error`; see [Fixing errors](#fixing-errors). Nextcloud
often answers a path or object the account cannot see like a missing one.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas, shown as `code: message`.

### Reading a refusal

A refusal by Nextcloud shows only Qatlas' own message and carries no `provider` part, so there is no provider answer
below it. Qatlas does not copy the text Nextcloud answered with: Nextcloud echoes the request path into it, and the
class plus the HTTP status is what you can act on. Read the message for what Qatlas knows, and look at the Nextcloud
log of the instance (as its administrator) for Nextcloud's own reason.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Nextcloud rejected the user ID or the app password | HTTP 401. The user ID or the app password is wrong, or the app password was revoked | Create a new app password ([Create an app password](#create-an-app-password)) and store it again with `qatlas credential set <credential> app-password`. Check that `user-id` is the user ID, not the e-mail address. |
| `auth`: the Nextcloud user ID is unusable | The stored user ID is empty or holds a character such as `/`, `\`, `:`, `%`, `?`, `#`, or a control character | Store the user ID again with `printf %s "$NEXTCLOUD_USER" \| qatlas credential set <credential> user-id`. |
| `auth`: the Nextcloud app password is unusable | The stored value is shorter than 8 characters or holds a space, a line break, or a character that is not printable ASCII | Store it again with `printf %s "$NEXTCLOUD_APP_PASSWORD" \| qatlas credential set <credential> app-password`. |
| `provider-error`: a Nextcloud service must use https (or a similar message about the address) | The address is `http`, or holds user information, a query, or a fragment | Use the plain `https://host` address, optionally with the installation path; see [Configuration](README.md#configuration). |
| `tls`: the TLS connection could not be established | The certificate of the instance is not trusted by this computer, or does not match the host name | Install a certificate from a trusted authority on the instance, or make this computer trust the authority that issued it. |
| `unreachable`: Nextcloud could not be reached | Wrong host, no network route, or the instance is down | Check the address, the network, and any proxy settings. |
| `unreachable`: Nextcloud is unavailable or in maintenance mode | HTTP 503 | Wait until the administrator ends the maintenance mode. |
| `permission`: this Nextcloud identity may not perform this operation on the path | HTTP 403. The account has no right to this path or action | Give the account the right in Nextcloud ([Files](#files)): read for reads, write for changes; or ask the owner of a shared item for a share with more rights. |
| `permission`: this Nextcloud identity is not an administrator or subadmin of the requested users or groups | The account is neither an administrator nor a subadmin of them | Make it an administrator, or a subadmin of those groups ([Administration](#administration)). |
| `permission`: this connection or identity may not change this Deck board | Nextcloud refused the change, or the connection does not bind the board | Give the account the right to manage the board, or add the board to the connection's targets; see [Targets](README.md#targets). |
| `permission`: this identity is not an owner or moderator of this Talk conversation | The change needs a moderator | Make the account a moderator of the conversation in Talk. |
| `permission`: this calendar (or address book, or note) is read-only for this Nextcloud identity, nothing was changed | The item is shared read-only | Ask the owner to share it with write access, or use an item the account owns. |
| `permission`: this ... belongs to someone else, nothing was changed | The call needs the owner of a calendar or address book | Use the account that owns it. |
| `permission`: this Nextcloud identity may change or delete only its own comments | The comment has another author | Not a right problem. Nextcloud allows only the author to change it. |
| `permission`: the instance policy or the rights of this Nextcloud identity do not allow this share | HTTP 403 on a share. Sharing is off, or the item may not be shared with this recipient | Ask the administrator to allow sharing or to lift the limit, or share an item the account may share. |
| `permission`: this Nextcloud identity may not create, change, or delete this system tag | HTTP 403 | Use an administrator account for system tags that are hidden or cannot be assigned. |
| `provider-error`: Nextcloud refused the visibility or assignability; only an administrator may create invisible or unassignable system tags | HTTP 400 on a tag change | Create the tag as visible and assignable, or use an administrator. |
| `provider-error`: Nextcloud refused this share; an instance policy (for example a required or too late expiry date) or the arguments do not allow it | HTTP 400 on a share | Give the expiry date the instance requires, or check the arguments. |
| `provider-error`: this Nextcloud connection does not hold this path | HTTP 404. The path does not exist, or the account cannot see it | Check the path, then the account's access to it. If every path answers this, check the installation path in the address. |
| `not-found`: the Nextcloud Deck app is not available, or the board, stack, or card does not exist | The Deck app is off, or the object is missing or not visible | Ask the administrator to enable Deck, or check the IDs and the board's sharing with the account. |
| The Talk app (spreed) is not available on this Nextcloud instance (`provider-error` or `not-found`) | The Talk app is off | Ask the administrator to enable Talk. |
| `not-found`: the Notifications, Activity, or User status app is off or not reachable for this identity | The app is off, or does not answer for the account | Ask the administrator to enable the app. |
| `provider-error`: federated sharing is not available on this Nextcloud instance | Federated sharing is off | Ask the administrator to enable it, or do not use the remote shares tools. |
| `provider-error`: the Nextcloud trash bin is not available; the files_trashbin app may be inactive | The trash bin app is off | Ask the administrator to enable `files_trashbin`. |
| `provider-error`: the storage quota of this Nextcloud identity is exhausted | HTTP 507 | Free space or raise the account's quota. |
| `provider-error`: this Nextcloud node is locked | HTTP 423. Another user or app holds a lock | Wait until the lock ends or ask the holder to release it. |
| `provider-error`: the Nextcloud file changed or already exists | HTTP 412. The file changed since it was read, or the target exists | Read the file again and use its new `etag`, or choose another name. |
| `provider-error`: Nextcloud answered with a redirect, which Qatlas does not follow | The address redirects, for example from a short host name or to another path | Use the final `https` address of the instance, including its installation path. |
| `rate-limited`: Nextcloud rate-limited the operation | HTTP 429. Too many requests or too many failed logins | Wait and retry. After failed logins, fix the credential first. |
| Test: this Nextcloud identity does not hold the root folder this connection is bound to | The bound folder does not exist for the account | Change the `folder/PATH` target or create the folder. |
| Test: this Nextcloud identity does not hold a calendar this connection is bound to | A bound calendar is missing for the account | Change the `calendar/URI` target; the picker lists the calendars. |
| Test: this Nextcloud identity may not read the configured root folder | The account has no read access to the folder | Give it read access to the folder. |

An outcome that ends unclear after a change is handled as under [Unclear outcomes](README.md#unclear-outcomes).
