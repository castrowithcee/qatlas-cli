---
description: >
  Describes the Matomo user tools: users and their roles, the access of users to websites, deleting and logging out users, and the roles and capabilities Matomo offers, with the personal data they carry and the account security data they never return.
type: knowledge
edit: shared
created: 2026-10-25
updated: 2026-10-26
---

# Matomo users and roles

The user tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope); the
rights and the plugin are listed in [rights per tool group](setup.md#rights-per-tool-group). Four tools
read; `matomo.users.list`, `matomo.users.get`, and `matomo.users.get-access` carry personal data and the rights of
administrators. `matomo.users.delete` and `matomo.users.logout` change users, and three more change roles and
capabilities. No user tool is in a setup profile.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.users.list` | reads the users of a website with their role, or the users of the instance (the instance-wide mode needs the target `instance`) |
| `matomo.users.get` | reads one user, checks a login or email, lists the Super Users, or tells whether the token is a Super User (target `instance`) |
| `matomo.users.get-access` | reads which websites a user can access, or which users can access a website (the raw and instance-wide modes need the target `instance`) |
| `matomo.users.delete` | deletes a user (target `instance`; effect `delete`, only via `tools`) |
| `matomo.users.logout` | ends the web sessions of a user (target `instance`; effect `execute`) |
| `matomo.access.set` | sets or removes the role of a user on websites (effect `update`, only via `tools`) |
| `matomo.capabilities.add` | adds capabilities of a user on websites (effect `update`, only via `tools`) |
| `matomo.capabilities.remove` | removes capabilities of a user on websites (effect `update`, only via `tools`) |
| `matomo.roles.list` | reads the roles and capabilities Matomo offers |
| `matomo.ldap-users.count` | counts the LDAP directory entries matching a filter or a group (target `instance`; only via `tools`) |
| `matomo.ldap-users.list` | lists the Matomo logins stored as LDAP users (target `instance`; only via `tools`) |

## Personal data and what is never returned

Logins, email addresses, and the dates of an account are personal data of other people and untrusted text. A user is
read through an allow-list: `login`, `email`, `date_registered`, `last_seen`, `last_seen_ago`, `superuser_access`,
`role`, and `capabilities`. Everything else in a Matomo user row is dropped, including members a later release adds.
Matomo 5 holds no alias for a user.

Data about the security of an account is not returned, whatever the token's rights: the password hash, authentication
tokens, the two-factor state and secret, the invitation (token, link, expiry, acceptance, status, inviter), the time
of the last password change, and failed attempts. For the same reason `matomo.users.list` offers no invitation status
filter (`filter_status`). Matomo itself shows the email address of other users only to Super Users.

## Choosing the method

Each tool chooses its Matomo method from the arguments; the caller never names one.

| Tool | Arguments | Method | Target |
| --- | --- | --- | --- |
| `matomo.users.list` | `idSite` | `getUsersPlusRole` | the website |
| | none or `logins` | `getUsers` | `instance` |
| | `only_logins` | `getUsersLogin` | `instance` |
| `matomo.users.get` | `login` or `email` | `getUser`, `getUserByEmail` | `instance` |
| | `kind` `exists` | `userExists`, `userEmailExists` | `instance` |
| | `kind` `superusers` | `getUsersHavingSuperUserAccess` | `instance` |
| | `kind` `token-superuser` | `hasSuperUserAccess` | `instance` |
| `matomo.users.get-access` | `login` | `getSitesAccessForUser` | websites; not for Super Users |
| | `login`, `raw` | `getSitesAccessFromUser` | `instance` |
| | `idSite` | `getUsersAccessFromSite` | the website |
| | `idSite`, `access` | `getUsersWithSiteAccess` | the website |
| | `access` alone | `getUsersSitesFromAccess` | `instance` |
| `matomo.roles.list` | `section` | `getAvailableRoles`, `getAvailableCapabilities` | websites |

`idSite` must lie inside the connection's targets and is refused
before a secret is read; it is never defaulted by a single bound website, because without it `matomo.users.list` and
`matomo.users.get-access` choose their instance-wide mode.

## Targets

`matomo.users.get` is instance-wide in every kind and is offered only to connections that name `instance`. Even the
existence check and the Super User check of the token are limited to it, because they let a caller probe the accounts
and rights of the whole instance. `matomo.users.list` and `matomo.users.get-access` are offered to connections that
reach websites; their instance-wide modes are refused in the tool, before a secret is read, unless the targets also
name `instance`. `matomo.roles.list` needs only a website binding.

Access rows of websites outside the targets are dropped one by one: the websites of `getSitesAccessForUser` and
`getSitesAccessFromUser`, and the websites of `getUsersSitesFromAccess`, where a user left without a website is dropped
too. With `site/ID` targets these rows are read up to 1000 and filtered here; a user with that many websites is refused
as an invalid request asking for a narrower `role` or `search`. The roles on a website are shown only for websites inside
the targets. The instance-wide lists carry no website data.

## Paging

Lists page like other lists. `getUsersPlusRole` and, with every website in scope, `getSitesAccessForUser` page at
Matomo; the other methods answer in full and are sorted by login and cut on the Qatlas side.

## Roles and capabilities

`matomo.roles.list` returns the roles a user can be given (`view`, `write`, `admin`; not `superuser` or `noaccess`) and
the capabilities, which plugins extend, with the roles that already include each. Names and descriptions are translated
by Matomo and untrusted; help addresses are text that is never fetched. The tool returns no personal data, but it is
not in the `read` profile.

## Deleting and logging out users

Both tools need a connection whose targets name `instance`, take one `login` (1 to 100 characters), and send exactly
one request that is never repeated. `anonymous` is refused before any request. A personal token needs no password
confirmation, and the tools take no password argument.

- `matomo.users.delete` is offered only where the connection's `tools` list names it. Any login the token may not
  delete is reported as not found. Matomo does not delete the
  only Super User and sends an email to the deleting user. Deleting cannot be undone.
- `matomo.users.logout` ends the web sessions of the user; personal tokens stay valid.

Both report the login and success only after Matomo confirmed. After an unclear result, read the user with
`matomo.users.get` before repeating.

## Changing roles and capabilities

`matomo.access.set`, `matomo.capabilities.add`, and `matomo.capabilities.remove` change an existing user on up to 100
websites (`idSites`), all inside the connection's targets; one outside, a repeated ID, or `all` refuses the whole call
before any request. They need no `instance` target. All three are offered only
where the connection's `tools` list names them, send exactly one request that is never repeated, and report success only
after Matomo confirmed. After an unclear result, read the access with `matomo.users.get-access` before repeating.

- `matomo.access.set` gives one `role` (`view`, `write`, `admin`) or removes the access (`noaccess`). It replaces every
  role and capability the user held on those websites; optional `capabilities` are given again with the role.
- `matomo.capabilities.add` needs a role on every website already; a capability the role includes is not stored.
- `matomo.capabilities.remove` removes only capabilities given separately; those a role includes stay effective.

Capability IDs come from `matomo.roles.list`; a role name is refused as a capability. Super User rights cannot be
granted, `anonymous` is refused, and the tools take no password: a personal token needs no password confirmation.

## LDAP users

The two tools are unsupported without the LoginLdap plugin. Both are instance-wide, are in no profile, and are offered only via `tools`. `matomo.ldap-users.count` takes exactly one
of `filter` (`getCountOfUsersMatchingFilter`) and `memberOf` (`getCountOfUsersMemberOf`); the filter text is sent to
Matomo, which queries the configured directory with its own bind account. An invalid filter is `invalid-request`, an
unreachable directory a provider error. `matomo.ldap-users.list` reads only the logins in the Matomo database
(`getExistingLdapUsersFromDb`) and does not contact the directory; logins are untrusted personal data.

The other LoginLdap methods are not offered: `saveLdapConfig` and `saveServersInfo` write the directory connection and
its credentials, and `synchronizeUser` creates or changes a user from the external directory.
