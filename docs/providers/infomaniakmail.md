---
description: >
  Describes the Infomaniak Mail provider: IMAP access to exactly one mailbox with a mailbox password, the
  required mailbox target and the optional folder and sender allow-lists, the folder and message-envelope
  reads, the read-only EXAMINE access, UID binding to folder and UIDVALIDITY, the fixed search criteria and
  their caps, and the boundaries of what this provider reads.
type: knowledge
edit: shared
created: 2026-10-01
updated: 2026-10-01
---

# Infomaniak Mail

Infomaniak Mail is a provider for exactly one Infomaniak mailbox over IMAP. It lists the mailbox's folders
and the envelopes of the messages in a folder. It is read-only: it reads no message body and no attachment,
changes no flag, moves, drafts, or deletes nothing, and sends nothing.

## Configuration

The connection always goes to `mail.infomaniak.com` on port 993 with implicit TLS, a verified certificate,
and TLS 1.2 or newer ([Infomaniak's IMAP settings](https://www.infomaniak.com/en/support/faq/2427/sync-your-emails-across-all-your-devices)).
No host, port, or TLS switch comes from the configuration or from a tool argument. `base_url` may be left out;
if it is set, it must be `https://mail.infomaniak.com`, and the IMAP host is never taken from it. The
Infomaniak REST mail API plays no part here.

The credential provides the role `password`: a mailbox password created for the mailbox in the Infomaniak
Manager. The login name is the mailbox address from the connection's `mailbox/` target.

```yaml
services:
  infomaniak-mail:
    provider: infomaniakmail

credentials:
  customer-a-mail:
    provider: infomaniakmail
    type: keyring
```

## Scope

| Target | Binds |
| --- | --- |
| `mailbox/ADDRESS` | the one mailbox this connection logs in to, also the IMAP login name; required, exactly one |
| `folder/NAME` | one folder by its exact name; optional, repeatable, at most 50 |
| `sender/ADDRESS` | one sender address; optional, repeatable, at most 50 |

```yaml
connections:
  customer-a-inbox:
    service: infomaniak-mail
    credential: customer-a-mail
    targets: [mailbox/office@example.com, folder/INBOX, folder/Invoices, sender/billing@example.net]
```

Addresses are plain ASCII addresses: no display name, no quoting, no internationalised domain, at most 254
bytes. A folder name is at most 255 bytes, valid UTF-8, without control characters, and without the IMAP
wildcards `*` and `%`, so a name can never widen into a pattern. `INBOX` is case-insensitive; every other
folder name is compared exactly. A folder target names that folder only; its subfolders are not implied.
No error quotes a configured value.

Without a folder target, every folder of the mailbox is reachable. With one, `infomaniakmail.messages.list`
refuses any other folder as an invalid request **before** a secret is read and before any connection is
opened, and the refusal never names an allowed folder. `infomaniakmail.folders.list` then asks the server only
for the allow-listed names and filters every answer again locally.

Without a sender target, every sender is listed. With one, only messages whose parsed From address is on the
list are returned: a message needs at least one From address, and every From address must be on the list. This
is checked locally on the parsed address of each envelope; a server-side SEARCH alone is never trusted for it,
since IMAP searches match substrings (a lookalike address or a display name that contains an allowed address
would match).

## Tools

| Tool | Effect | Confirmation | Does |
| --- | --- | --- | --- |
| `infomaniakmail.folders.list` | read | none | IMAP LIST, filtered to the folder allow-list |
| `infomaniakmail.messages.list` | read | none | envelopes of the newest matching messages of one folder |

The one recommended profile, `read`, offers both tools. A connection needs the `read` permission.

## Listing messages

`infomaniakmail.messages.list` takes a `folder` and these fixed, typed filters only:

| Argument | Meaning |
| --- | --- |
| `since`, `before` | received on or after, or before, a date as `YYYY-MM-DD` (the server compares dates only) |
| `unread` | only messages without the `\Seen` flag |
| `sender` | one address; with a sender allow-list it must be on it |
| `uid_from`, `uid_to`, `uidvalidity` | a UID window, see below |
| `limit` | 1 to 100 messages; 25 when omitted |

There is no free search string and no raw IMAP command: nothing but these typed values ever reaches the
SEARCH. Results are the newest matches first. `matched` is the number of hits before `limit`, and `has_more`
is true when older matches were left out.

The folder is opened with `EXAMINE`, not `SELECT`, and envelopes are fetched with `ENVELOPE`, `FLAGS`, and
`RFC822.SIZE`, so a listing never changes a message's `\Seen` flag.

Each message carries only envelope data: `uid`, `date`, `from`, `to`, `subject`, `flags`, and `size`. Every
string has its control characters replaced and is capped (subject 256 characters, names 128, at most 10 From
and 20 To addresses, at most 20 flags).

## UIDs belong to a folder and a UIDVALIDITY

A UID means something only together with its folder and the folder's `UIDVALIDITY`, and every answer names
both. A request that refers to a UID window (`uid_from` or `uid_to`) must repeat the `uidvalidity` of the
earlier answer; if the folder's current `UIDVALIDITY` differs, the request is refused as invalid and nothing
is listed, because the old UIDs may now belong to other messages. `uidvalidity` without a UID is refused. When
both ends of the window are given, it spans at most 10000 UIDs; a window with only `uid_from` runs to the last
message, and the result count is still capped by `limit`.

## Errors

Errors keep stable classes. The text an IMAP server answers with never reaches an error message, and neither
does the password.

| Class | Cause |
| --- | --- |
| `auth` | the mailbox address or password was rejected, or the password is unusable |
| `permission` | the mailbox may not perform the operation |
| `not-found` | the mailbox holds no such folder, or does not show it |
| `unreachable` | the server could not be reached, is unavailable, or closed the connection |
| `tls` | the TLS connection or certificate check failed |
| `timeout` | the server did not answer in time (30 seconds per operation) |
| `rate-limited` | a rate limit asks to wait longer than the request may take |
| `invalid-provider-response` | an answer lacked an envelope |
| `provider-error` | every other rejection |

A folder or sender outside the connection's targets, a malformed argument, and a `uidvalidity` that no
longer matches are invalid requests, never provider errors.

## Untrusted data

Subjects, names, addresses, and every other value of a message come from third parties and are untrusted data.
They carry their own data sensitivity class, `infomaniak-mail-messages` (folder names use
`infomaniak-mail-folders`). Qatlas cleans and bounds them and never renders, follows, or executes anything
derived from them. Each operation opens one connection and closes it; a failed login is not retried.

## Boundary

This provider lists folders and message envelopes. It does not read message bodies or attachments, change
flags, move, copy, draft, or delete messages, create or rename folders, or send mail (SMTP); those are out of
scope.
