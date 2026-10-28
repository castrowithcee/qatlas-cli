---
description: >
  Describes Nextcloud share tools: shares, sharees, incoming and remote shares, and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-28
---

# Nextcloud shares

Group `shares`: sharing of files and folders.

The tool group `shares` reads and manages sharing. `nextcloud.shares.shares.list` and `nextcloud.shares.shares.get` (profiles
`read` and `write`) need a `folder` target. A share counts only when its item lies at or below the root: the
`path` of a share the identity owns, or the `file_target` of an incoming one (`shared_with_me`). `list` drops
every other share; `get` answers one outside the root like a missing one. A share the identity passed on without
owning it is neither own nor incoming and is not reported. Without `path`, `list` reads and filters all shares;
with `path` it reads that node, with `subfiles` the items of that folder.

All share types are reported with a stable type name (`user`, `group`, `link`, `email`, `federated`, `team`,
`talk`, other types as `other`), rights as flags, expiry, note, and label. A share never reports its link
token, its link URL, or any password; `has_password` only says that one is set. The recipient ID is reported
for `user`, `group`, `email`, `federated`, and `team` shares; a `talk` share reports only the conversation name
and `link` and `other` shares no recipient. Recipients are personal data and untrusted.

`nextcloud.shares.sharees.search` and the `nextcloud.shares.incoming-shares.*` and `nextcloud.shares.remote-shares.*` tools are
account-wide: they need an `account` target, not a folder, and without one refuse locally, before any credential
access or request.
`shares.sharees.search` reports only type, identifier, and display name of possible recipients, never Talk
conversations or unknown types (their identifier may be an access token), never uses the global lookup server,
asks for at most 50 candidates per kind (default 10), and is in no setup profile.

`shares.incoming-shares.pending` lists the offers the identity has not accepted, from other users of the instance
(internal) and from federated servers (host only), with origin, sender, name, and type. Two limits come from the
API: a federated offer carries no share time (its `mtime` is a file time), so none is reported; an internal one
carries its share time but no status, so offers declined earlier are listed like open ones and can be accepted
again. `accept` takes internal offers only. `decline` names the origin and needs a tools list. Both read the
pending list of that origin once, refuse an ID not pending there without naming anything else, and send exactly
one request; IDs of the two origins are independent, so `accept` refuses a federated ID like an unknown one.

`shares.remote-shares.list`, `get`, and `remove` read and remove the federated shares the identity accepted; none is
created or accepted here. A share reports ID, owner, name, its mount point relative to the Files root, and the
other server only as a host, never a token, password, or remote URL. An instance without federated sharing
answers `list` with a clear error. `remove` needs a tools list, is in no profile, reads the share once, and
refuses an unknown ID without naming a target.

`nextcloud.shares.shares.create`, `nextcloud.shares.shares.update`, and `nextcloud.shares.shares.delete` need a `folder` target, a
confirmation, and a tools list; no profile contains them. `create` shares one existing item below the root, never
the root itself, with one existing user or group (`type` `user` or `group`; find IDs with `shares.sharees.search`).
Reading is always granted; the other rights are flags that default to false. Links, e-mail, federated, and team
shares cannot be created or changed, Talk shares only by `talk.files.share`; no password or label is set. `update`
changes rights, expiry, or note of a `user` or `group` share the identity made; rights not given keep their value.
`delete` revokes any share the identity made below the root, whatever its type; shares made to it stay untouched.

`update` and `delete` read the share once and refuse without a change a share outside the root, a share that
is not the identity's own, and (for `update`) any other type; the refusal names no path. They then send exactly
one request. After an unclear outcome (timeout, aborted connection, a 5xx or unreadable answer) the error says
the change may have been applied and to check the share (`shares.shares.get`, `shares.incoming-shares.pending`, or
`shares.remote-shares.get`) before repeating; Qatlas never repeats. A refusal by the instance (for example a required
expiry or disabled sharing) is a clear error without the text of the instance.

## Exclusions

The rules are in [Exclusions](README.md#exclusions). Every answered share is checked against them by its `path` and
its `file_target`, incoming shares included. A share on or below an excluded folder is not listed, `get` answers
it like a missing share, and `update` and `delete` refuse it like one outside the root. A share on a folder above
an excluded one, the root included, is also hidden in `list` and `get`, and `update` treats it as missing, but
`delete` still revokes it, so access is never left that cannot be taken back. `create` refuses a path on or below
an excluded folder like a missing item, and a path above one with `permission`, before any credential access;
the refusal names no path.
