---
description: >
  Describes the confirmed Infomaniak Mail message changes (flag, move, trash, expunge): their risk, tools list
  requirement, folder binding, fixed values, the one-change-no-retry rule, required server features, and the
  trash and expunge behaviour.
type: knowledge
edit: shared
created: 2026-10-01
updated: 2026-10-27
---

# Changing a message

The change tools of [Infomaniak Mail](README.md).

## Changing a message

The four change tools take `folder`, `uid`, and `uidvalidity` as the reads do, all required, plus:

| Tool | Further arguments | Answer |
| --- | --- | --- |
| `messages.flag` | `flag`: `seen` or `flagged`; `set`: `true` sets, `false` removes | `folder`, `uidvalidity`, `uid`, `flag`, `set`, and the `flags` the server reports after the change |
| `messages.move` | `destination`: the exact name of another folder | `folder`, `uidvalidity`, `uid`, `destination`, and `destination_uid` with `destination_uidvalidity` when the server reports them |
| `messages.delete` | none | as `messages.move`, with the trash folder as `destination` |
| `messages.expunge` | none | `folder`, `uidvalidity`, `uid` |

Rules that hold for every change:

- **Risk.** Each carries a complete risk: `flag` is `update` and idempotent; `move` is `update`, `delete` and
  `expunge` are `delete`, and all three are non-idempotent. All are confirmed (the request needs `confirm`),
  open-world, and of the class `infomaniak-mail-messages`.
- **Tools list.** All four require the connection's tools list, whatever the effects: a connection that selects
  by `effects` offers none of them. `delete` and `expunge` because they remove a message from its folder or
  from the mailbox; `move` because it changes the UID and folder of a message, which every earlier reference
  loses; `flag` is held to the same rule because it changes mailbox state the person sees in every client, and
  the rule is the narrower of the two readings.
- **Folders.** The source folder and, for `move`, the destination must both be inside the folder targets and not
  excluded. A folder outside them, a destination equal to the source, a wildcard, and a missing `uid` or `uidvalidity`
  are refused as invalid requests **before** a secret is read and before any connection is opened, without naming the
  foreign or any allowed folder. After the folder is opened, the `uidvalidity` is compared and the sender list applies:
  a message from a sender outside the list answers `not-found`, exactly like a missing UID, and nothing changes.
- **SELECT, not EXAMINE.** A change opens its folder with `SELECT`; the message is read with `ENVELOPE`,
  `FLAGS`, `RFC822.SIZE`, and `BODYSTRUCTURE` only, so preparing the change sets no flag. Reads keep using
  `EXAMINE`.
- **Fixed values.** Only `seen` and `flagged` can be changed, mapped by the provider to `\Seen` and
  `\Flagged`; the IMAP flag never comes from an argument. No IMAP command, folder pattern, or search string
  comes from an argument.
- **One change, no retry.** After the reading preparation, a change sends exactly one changing request
  (`UID STORE`, or `UID MOVE`); `expunge` sends the one `UID STORE +FLAGS.SILENT \Deleted` that `UID EXPUNGE`
  needs, then `UID EXPUNGE` for the same UID. A tagged `NO` or `BAD` means nothing was changed. A timeout, a
  dropped connection, a server `BYE`, or an unreadable answer leaves the outcome unknown; the error then says
  that the change may have been applied and is never repeated by Qatlas. If `expunge` fails after its
  `STORE`, the error says the message may be marked deleted without being removed.
- **Server features.** `move` and `delete` need `MOVE`; `expunge` needs `UIDPLUS`. A server that lacks one is
  refused with a `provider-error` that says nothing was changed. The library's fallback (`COPY`, mark, and a
  folder-wide `EXPUNGE`) is never used.

**Trash.** `messages.delete` moves the message to the one folder the server marks with the SPECIAL-USE attribute
`\Trash` in `LIST`. No folder is chosen by name. If the server does not offer `SPECIAL-USE`, marks no folder or
more than one as `\Trash`, or the trash folder is outside the connection's folder targets or excluded, the call is
refused as an invalid request without naming a folder and nothing is sent that changes the mailbox. A message
that is already in the trash folder is not deleted again; use `messages.expunge`.

**Expunge.** `messages.expunge` removes one UID for good and cannot be undone. It marks only that message
`\Deleted` and sends `UID EXPUNGE` for that one UID, never a plain `EXPUNGE`, so other messages that are
marked `\Deleted` stay.

```yaml
connections:
  customer-a-inbox:
    service: infomaniak-mail
    credential: customer-a-mail
    targets: [mailbox/office@example.com, folder/INBOX, folder/Archive, folder/Trash]
    tools: [infomaniak.mail.messages.list, infomaniak.mail.messages.get, infomaniak.mail.messages.flag,
            infomaniak.mail.messages.move, infomaniak.mail.messages.delete]
```
