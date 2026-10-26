---
description: >
  Guides a user without special knowledge through setting up the BookStack provider: creating an API token, adding it to Qatlas, checking the connection, the BookStack permissions each tool group needs, what happens without a permission, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up BookStack

This page shows how to connect Qatlas to BookStack, which BookStack permissions the token's user needs, and what
to do when BookStack refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[BookStack provider](README.md) and the page of each tool group; this page names only the permissions.

**The token's user decides what is possible at all.** A BookStack API token has no rights of its own: it acts as
the user who owns it, with that user's roles. The rights you set in Qatlas (the connection's `tools`, `effects`,
and `targets`) limit it further. A call works only if both allow it. A connection can never do more than the user
behind its token, and a token never widens the connection.

## What you need

- The address of your BookStack instance. It must start with `https://`; Qatlas refuses `http` so the token is
  never sent in clear text. See [Credential and base URL](README.md#credential-and-base-url).
- A BookStack user that can see what Qatlas should work with, and whose role holds **Access System API**
  (`access-api`). Without it, BookStack shows no API token section and refuses every API call.
- Prefer a user of its own for Qatlas, with a role that holds only what the connection's tools need. Then the
  token of a connection that only reads belongs to a user that can only read.
- Qatlas keeps the token ID and the token secret in the system keyring or the vault, or reads them from
  environment variables.

## Create a token

1. Sign in to BookStack as the user the token should act as. An administrator can first add **Access System API**
   to a role of that user under **Settings**, then **Roles**.
2. Open your profile (the user menu, then **My Account**) and find **API Tokens**. The section appears only if
   the user may access the API.
3. Choose **Create Token**, enter a name you will recognize, for example `qatlas-reader`, and an expiry date.
   A short expiry is safer; you replace the token when it ends.
4. Save, and copy the **Token ID** and the **Token Secret** at once. BookStack shows both right after saving.
   The token ID is generated; it is not the name you chose.

BookStack changes its pages from time to time; look for these names. The interface of your instance and its
`/api/docs` page are authoritative.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `bookstack`, the service with the instance
   address, a credential, the scope (targets), and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and enter the two values as the secrets
   `token-id` and `token-secret`.

Without the guided setup, store both values on the command line. Each is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$BOOKSTACK_TOKEN_ID" | qatlas credential set bookstack-reader token-id
printf %s "$BOOKSTACK_TOKEN_SECRET" | qatlas credential set bookstack-reader token-secret
```

`bookstack-reader` is the name of the credential in your configuration. To replace a token that expired or was
revoked, run both commands again with the new values. Run `qatlas config validate` to check the configuration
file. It contacts no provider.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means BookStack accepted the token
for one read of the page list (a single entry).

**A passing test does not mean every tool works.** It shows that the token is valid, not expired, and may use the
API. BookStack checks the permissions again on every call. Compare the user's role with
[Permissions per tool group](#permissions-per-tool-group) for the tools you use. A failing test or call is
explained under [Fixing errors](#fixing-errors).

## Permissions per tool group

Find your tool group and add the permission to a role of the token's user (**Settings**, **Roles**). Every group
needs **Access System API** (`access-api`) first. The names below are the BookStack role permission names Qatlas
uses; the role form of your instance shows them as labels.

- **Content permissions** are the per-object rights of a role: view, create, update, and delete, each for all
  items or only the user's own. They can be narrowed per shelf, book, chapter, and page, so a role that holds a
  right in general can still be refused on one item. See BookStack's
  [roles and permissions](https://www.bookstackapp.com/docs/user/roles-and-permissions/).
- **System permissions** are instance-wide rights such as `content-export` or `users-manage`.
- A tool offered only when a connection's `tools` list names it (for example a delete) needs the same
  permissions as its group, unless a row says more.
- BookStack decides on every request. The object permissions in the tables follow the names of its role form; where
  BookStack documents no exact mapping, the role form of your instance is authoritative.
- Content a user cannot view is not returned: lists stay empty or shorter, and a direct read answers `not-found`.

### Content

| Tools | Permissions of the token's user |
| --- | --- |
| Read pages, books, chapters, shelves, search, tags | View permission on the content (`page-view-all`, `book-view-all`, `chapter-view-all`, `bookshelf-view-all`, or the `-own` forms) |
| Create pages, books, chapters, shelves | The create permission of that kind (for example `page-create-all` or `page-create-own`) |
| Update pages, books, chapters, shelves, set covers | The update permission of that kind (for example `page-update-all`) |
| Move a page or chapter into another book or chapter | Update and delete permission on the item, and create permission in the target |
| Delete pages, books, chapters, shelves | The delete permission of that kind (for example `page-delete-all`) |
| Export (`content.export`, `content.download`) | `content-export`, in addition to view permission |

Moving fails as `permission` without the delete permission, although the update permission is held. Everything in
this group follows the object permissions of the content, so a missing right on one book does not affect another.

### Comments

| Tools | Permissions of the token's user |
| --- | --- |
| Read comments | View permission on the page |
| Create a comment | `comment-create-all` |
| Update a comment | `comment-update-all` or `comment-update-own` |
| Delete a comment | `comment-delete-all` or `comment-delete-own` |

### Attachments and images

| Tools | Permissions of the token's user |
| --- | --- |
| List, read, and download attachments and images | View permission on the page |
| Upload or link an attachment, upload an image | `attachment-create-all`; `image-create-all` |
| Update or replace an attachment or an image | `attachment-update-all` or `attachment-update-own`; `image-update-all` or `image-update-own` |
| Move an attachment to another page | Update permission on both pages |
| Delete an attachment or an image | `attachment-delete-all` or `attachment-delete-own`; `image-delete-all` or `image-delete-own` |

### Imports

| Tools | Permissions of the token's user |
| --- | --- |
| Upload, list, read, run, and delete imports | `content-import` |

### Administration

These tools cover the whole instance. Give them a connection and a token of their own that no other connection
uses, with a dedicated user whose role holds only the permissions of the tools that connection lists.

| Tools | Permissions of the token's user |
| --- | --- |
| Instance information | None beyond **Access System API** |
| Read content permissions of an item | The permission to manage content permissions (`restrictions-manage-all`, or `restrictions-manage-own` for own items); as far as BookStack documents it |
| Change content permissions of an item | The same, and for Qatlas' guest-role check also `user-roles-manage` |
| List and restore the recycle bin, destroy a deletion | `settings-manage` and `restrictions-manage-all` |
| List or read users | `users-manage` |
| Change or delete users | `users-manage`, and `user-roles-manage` when `roles` or `migrate_ownership_id` is given (for the guest-role check) |
| List or read roles | `user-roles-manage` |
| Create, update, or delete roles | `user-roles-manage` |
| Read the audit log | `settings-manage` and `users-manage` |

Role permissions can escalate to full control of the instance, so a token that may change roles or users is
as strong as an administrator. Do not hand it to a connection that does not need it.

## What happens without a permission

The tool fails with the code `permission`, `auth`, or `not-found`; see [Fixing errors](#fixing-errors). BookStack itself returns only the
content the user may view.

Some tools read more than the one thing you asked for, and fail when that read is refused:

- A tool that changes content permissions, roles, or users may first read the role list to find the guest role.
  Without `user-roles-manage` it changes nothing and says that the guest role could not be determined.
- On a connection with targets, Qatlas reads the object once to prove it is inside the targets. A refusal of
  that read fails the call.

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas. Qatlas never relays the text BookStack sent: there
is no second "provider answer" part. For an invalid input, the message names at most the arguments BookStack
rejected, never their values. The status-to-code table is under [Errors](README.md#errors).

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: BookStack rejected the API token | HTTP 401. The token ID or secret is wrong, the token was deleted, or it belongs to another BookStack than the instance address | Create a new token ([Create a token](#create-a-token)) and store both values again with `qatlas credential set <credential> token-id` and `token-secret`. Check that the address names the right instance. |
| `auth`: BookStack refused the API token (connection test) | HTTP 403 on the test. The user lacks **Access System API**, or the token expired | Add `access-api` to a role of the user, or create a new token with a later expiry. |
| `provider-error`: a BookStack service needs a usable https URL | The address is `http`, or holds user information, a query, or a fragment | Use the plain `https://host` address, optionally with the installation path such as `https://host/wiki`. Put TLS in front of the instance if it has none. |
| `permission`: the token's user lacks the BookStack role permission for this action, or the token expired or lacks API access | HTTP 403 on a call | Find the tool group in [Permissions per tool group](#permissions-per-tool-group) and add the permission to a role of the user. If every call fails, check `access-api` and the token's expiry first. |
| `permission` when moving a page or chapter | The update permission is held, but not delete on the item or create in the target | Add the delete permission of that kind and the create permission in the target. |
| `permission` on an export | Missing `content-export` | Add `content-export` to a role of the user. |
| `permission` on an import | Missing `content-import` | Add `content-import`. |
| `permission` on users, roles, the audit log, or the recycle bin | Missing system permission | Add the permissions of [Administration](#administration). |
| Content permission, role, or user change fails with "the guest role (public access) of the instance could not be determined" | Qatlas could not read the role list; the class follows the failed read | Add `user-roles-manage`. Nothing was changed. |
| `not-found`: BookStack does not hold this resource or does not show it to this token | The identifier is wrong, **or** the user may not view it. BookStack answers both the same | Check the identifier. Then give a role of the user the view permission for that shelf, book, chapter, or page, and check for a restriction on that item. |
| `invalid-request` naming the targets | The shelf, book, chapter, or page is outside the connection's targets | Not a permission problem. Pass an item the connection allows, or add it; see [Targets](README.md#targets). |
| `rate-limited`: BookStack rate-limited the operation | HTTP 429 | Wait and retry. A change is never retried by Qatlas. |
| `provider-error`: BookStack rejected the input (HTTP 422), with `invalid: ...` | BookStack refused the value of the named argument | Correct the named argument; see the page of the tool group. |
| Error text ending "this change may have taken effect, read the current state in BookStack before repeating it" | A timeout, a lost connection, or a 5xx answer after the request was sent | Read the current state in BookStack before you repeat the change. |
