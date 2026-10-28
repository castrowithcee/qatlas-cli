---
description: >
  Describes BookStack instance information, content permission, recycle bin, user, role, and audit log tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# BookStack administration

Group `administration`. The token rights are listed under [Administration](setup.md#administration).

## Instance information

`bookstack.system.get` returns the instance information including the address of the logo, which is
never fetched and is untrusted data. It returns no content and therefore also works on a connection with targets.

## Content permissions

`bookstack.content-permissions.get` reads the permission overrides of one `page`, `chapter`, `book`, or
`bookshelf` (`type` and `id`): the owner (`id`, `name`, `slug`), the role overrides (`role_id`, `display_name`,
`view`, `create`, `update`, `delete`; at most 100, otherwise `truncated`), and the fallback permissions
(`inheriting`, plus the four values when not inheriting) that apply to every role without an override. Owner and
role names are personal data and untrusted provider content.

`bookstack.content-permissions.update` changes those permissions for existing roles in one request. Every
category is optional, but at least one is required, and BookStack applies exactly what is sent:

- `owner_id`: the new owner (a positive user id).
- `role_permissions`: replaces all role overrides. At most 100 entries with a unique positive `role_id` and all
  of `view`, `create`, `update`, `delete`. A category that is left out stays unchanged; an empty list removes
  all role overrides.
- `fallback_permissions`: `inheriting`, and all four values when `inheriting` is `false` (no values when it is
  `true`).

Public access is never opened. BookStack stores the fallback as an override that applies to every role without
an entry of its own, guests included, so Qatlas protects the guest role (the system role `public`). Before it
changes anything it determines that role (a read of the role list, at most 100 roles, and a read of the role to
confirm its system name and list its users), reads the current permissions of the item, and refuses locally,
without sending the change, when

- an entry for the guest role sets any value to `true`, unless the entry is identical to the guest entry that
  already exists,
- `role_permissions` is sent and omits an existing guest entry (also an empty list while a guest entry exists),
- `fallback_permissions` differs from the current fallback and the resulting role overrides (the sent
  `role_permissions`, otherwise the current ones) have no explicit guest entry with all four values `false`, or
- `owner_id` is a user of the guest role, because ownership would pass to anonymous visitors.

If the guest role cannot be determined (for example the role list cannot be read, no role has
the system name `public`, or the confirmation differs), the change is refused as well.
The refusals never name the other target and their message has no text of BookStack; its answer to the failed
role read goes separately as the original answer. The guest lookup is made once per call and not cached beyond it.

The tool requires confirmation and a tool allow list, is idempotent, is part of no profile, and sends exactly
one `PUT` without retry; after a timeout or a 5xx answer the result is reported as uncertain. On a connection
with targets or exclusions the item is proven first (see [Targets](README.md#targets)).

Exclusions: reading or changing the permissions of an excluded object is refused. BookStack passes the
permissions of a book or chapter on to everything below it, so a change to a book or chapter is also refused while
an excluded chapter or page lies below it; Qatlas reads the content tree for that, and a tree it cannot read
refuses the change. A read of such a book or chapter stays allowed. A shelf does not pass its permissions to its
books, so a shelf's permissions stay changeable unless the shelf itself is excluded.

## Recycle bin

`bookstack.recycle-bin.list` lists the deleted items of the instance (`limit`, `offset`, `sort`, `filters`). Each entry carries `id`
(the deletion identifier) and, of the deleted item, its metadata as BookStack reports it (including the creator, editor, owner, and the parent), never its
content. Names and descriptions are untrusted data.

`bookstack.recycle-bin.restore` restores one deletion by `deletion_id`.
`bookstack.recycle-bin.destroy` destroys one deletion permanently; this cannot be
undone.
The recycle bin spans the whole instance, so the tools are refused on a connection with targets. On a
connection with exclusions, `list` leaves out an entry that belongs to an excluded object, by its own type and
identifier, its `book_id` or `chapter_id`, or its parent, and `restore` and `destroy` refuse such an entry. A
deleted book is no longer on its shelf, so with an excluded shelf a deleted book, and an entry whose book is
deleted, counts as excluded unless it can still be read. `restore` and `destroy` of a deleted book or chapter
also take everything deleted with it, which the API does not list: they are refused unless every excluded chapter
or page that could lie below the entry is still readable and lies in another book or chapter, and they are
refused for an entry the list does not show. Each change sends exactly one request (`PUT` or `DELETE`) without retry; after a timeout or a 5xx answer the result is
reported as uncertain. The tools are part of no profile, and `destroy` additionally requires a tool allow list
that names it and the `delete` effect.

## Users, roles, and audit log

These tools cover the whole instance, so they are refused on a connection with targets (before any secret is
read), are part of no profile, and are classified as `bookstack-people` data (e-mail addresses, external
authentication identifiers, IP addresses). Names and texts are untrusted data. Exclusions do not reach users and
roles: a role applies to the whole instance, so changing one or a user's roles also changes the access to
excluded content.

`bookstack.users.list` (`limit`, `offset`, `sort`, `filters`) and `bookstack.users.get` (`id`) read users. BookStack does not report roles in the
listing, so only `get` carries them. The avatar and edit addresses are returned as untrusted addresses of the instance and are never fetched. `last_activity_at` and `created_at`
can be sorted but not filtered: the listing joins the latest activity as `last_activity_at`, so a filter on the alias
fails and one on `created_at` is ambiguous. A filter on `email` or `external_auth_id` narrows only what the tool
already lists.

`bookstack.roles.list` (`limit`, `offset`, `sort`, `filters`) and `bookstack.roles.get` (`id`) read roles. `get` returns the
`permissions` and the `users` of the role besides the listed fields, at most 1000 users; `truncated` is true when the
users or permissions were cut. `users_count` and `permissions_count` can be sorted but not filtered: BookStack
computes them as aliases of counting subqueries, and the database does not accept such an alias in a filter
condition. Without `sort`, the list is ordered by `id`, which is not a listed field and so no `sort` value.

### Changing roles

`bookstack.roles.create`, `bookstack.roles.update`, and `bookstack.roles.delete` change roles. Role permissions can escalate up to full administrative control of the instance, so these
tools require a tool allow list, are part of no profile, need confirmation, and are refused on a connection with
targets. Each sends exactly one change request without retry; after a timeout, a 5xx answer, or an unreadable
answer the result is reported as uncertain. `create` and `update` return the role like `roles.get`.

- `roles.create` takes `display_name` (3 to 180 characters, required), `description` (at most 180 characters),
  `mfa_enforced`, and `permissions`.
- `roles.update` takes `id` and the same fields; at least one must be given and a field that is left out stays
  unchanged. `permissions` replaces the whole list, so an empty list removes every permission.
- `roles.delete` takes `id` and is final: the users of the role lose it, the content permissions of the role are
  removed, and the API offers no migration of the users to another role. BookStack refuses to delete the
  registration role; Qatlas also reads the role first and refuses every system role (a role with a system name,
  such as `admin` or `public`) without a change.

`permissions` accepts only names from a fixed list of the permissions that the role form of BookStack v26.09.1
offers: `access-api`, `content-export`, `content-import`, `editor-change`, `receive-notifications`,
`restrictions-manage-all`, `restrictions-manage-own`, `settings-manage`, `templates-manage`, `user-roles-manage`,
`users-manage`, `revision-view-all`, and the content permissions `book-`, `bookshelf-`, `chapter-`, and `page-`
(`view`, `update`, `delete`, and `create`, each with `-all` and `-own`, except that `book-` and `bookshelf-` have
no `create-own`), and `image-`, `attachment-`, and `comment-` (`create-all`, `update-all`, `update-own`,
`delete-all`, `delete-own`). Unknown or repeated names are refused before any secret is read or request is sent.

The guest role (system role `public`, public access) is never changed. `roles.update` reads the role and
determines the guest role (a read of the role list, at most 100 roles, and a read of that role) and refuses,
without a change, when the role is the guest role or when the guest role cannot be determined.

`external_auth_id` is not supported: Qatlas never binds a role to the groups of an external identity provider,
so the argument does not exist and no request body contains it.

### Changing and deleting users

`bookstack.users.update` and `bookstack.users.delete` change or remove existing users. They require a tool allow list, are part of no profile, need confirmation, and are
refused on a connection with targets (before any secret is read). Each sends exactly one change request
without retry; after a timeout, a 5xx answer, or an unreadable answer the result is reported as uncertain. No tool
creates or invites users or sets an e-mail address, a password, or an external authentication identifier, and no
tool creates API tokens: these arguments do not exist, unknown arguments are refused, and no request body contains
them.

- `users.update` takes `id` and at least one of `name` (1 to 100 characters), `language` (1 to 15 letters, digits,
  hyphens, or underscores), and `roles` (at most 100 distinct positive role identifiers). A field that is left out
  stays unchanged; `roles` replaces all roles of the user, so an empty list removes every role. It returns the
  user like `users.get`.
- `users.delete` takes `id` and optionally `migrate_ownership_id`, which must differ from `id`. It is final.
  Without `migrate_ownership_id` the content of the user stays without an owner; with it, the ownership passes to
  that user, for all of the user's content, excluded content included. BookStack refuses to delete the only
  administrator and the guest user.

Public access is protected. When `roles` is given, `users.update` determines the guest role (system role `public`;
a read of the role list, at most 100 roles, and a read of that role) and
refuses, without a change, when the user is a guest user, when `roles` contains the guest role, or when the guest
role cannot be determined. `users.delete` does the same check when `migrate_ownership_id` is given and refuses
to hand the content to a guest user. Without `roles` or `migrate_ownership_id` no role is read.

`bookstack.audit-log.list` lists the audit log. It contains IP addresses and sign-in events
of all users, so it is offered only by a connection whose `tools` list names it. It takes `limit`, `offset`, `sort`, `filters`, and
the arguments `type` (lowercase letters, digits, and underscores, at most 64 characters), `user_id`,
`loggable_type` (`page`, `chapter`, `book`, or `bookshelf`), `loggable_id`, and `created_after`/`created_before`
(`YYYY-MM-DD` or RFC 3339; dates are UTC and a date alone means the start of that day). Values are checked before
any request. The arguments and `filters` all must hold; `ip` is personal data, and a filter on it widens neither
the connection nor the tool allow list.

Exclusions: on a connection with exclusions, entries whose `loggable_type` and `loggable_id` name an excluded shelf,
book, chapter, or page are left out, and so are those of a page or chapter in an excluded container. The
container is read per page or chapter; one that cannot be read, such as a deleted page, is left out as soon as a
container can be excluded. Entries without `loggable_type` and `loggable_id`, such as comment, attachment, and
image events, cannot be assigned and stay, and so do sign-in events; their `detail` text can still name excluded
content.
