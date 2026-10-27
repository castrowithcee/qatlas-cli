---
description: >
  Describes the Infomaniak Mail read tools: listing message envelopes of a folder with typed filters and
  newest-first order, UID binding to folder and UIDVALIDITY, reading one message with bounded text and
  attachment metadata, the limits on untrusted envelope and attachment fields, and reading one attachment
  inline or into a local file.
type: knowledge
edit: shared
created: 2026-10-01
updated: 2026-10-27
---

# Reading messages

The read tools of [Infomaniak Mail](README.md). Reading changes nothing, see
[Reading changes nothing](README.md#reading-changes-nothing).

## Listing messages

`infomaniak.mail.messages.list` takes a `folder` and typed filters, all combined with AND. The server compares dates
only. Nothing but these typed values ever reaches the SEARCH; there is no raw IMAP command. The descriptor lists the
filters with their bounds and exclusions. With a sender allow-list, the `sender` filter must name an address on it, and
never an excluded one. Results are the newest matches first. `matched` is the number of hits before `limit` and before
the messages of excluded senders are left out, so it can exceed what is listed; and `has_more` is true when older
matches were left out.

Limits of the filters:

- SORT and THREAD are extensions (RFC 5256) outside RFC 3501 and are not offered; the list is sorted by UID,
  descending.
- STATUS is a separate command for folder counters and does not belong to the message list.
- NEW, OLD, and RECENT are not offered: the `\Recent` flag no longer exists in IMAP4rev2 (RFC 9051).
- NOT and OR are not offered as a free expression tree: all filters are combined with AND, and every flag
  condition can be written in both directions with the three-valued arguments.
- Text and header filters only narrow the server search. The sender allow-list still checks every parsed From
  address locally, also when a `header` filter names `From`.

The folder is opened with `EXAMINE`, not `SELECT`, and envelopes are fetched with one `FETCH` of `ENVELOPE`,
`INTERNALDATE`, `FLAGS`, and `RFC822.SIZE`, so a listing never changes a message's `\Seen` flag.

Each message carries only envelope data and `INTERNALDATE`; the descriptor lists the fields. Every string has
its control characters replaced and is capped (subject 256 characters, names 128, at most 10 From and 20 To
addresses, at most 20 flags), and every list is bounded. A list that was cut is marked with
a `<field>_truncated` member. Nothing the server left out is filled in.

The `sender` argument is the From filter. The `sender` field is the ENVELOPE Sender header. They are
different things, and the allow-list always applies to the From addresses, not to Sender, Reply-To, Cc, or
Bcc.

## UIDs belong to a folder and a UIDVALIDITY

A UID means something only together with its folder and the folder's `UIDVALIDITY`, and every answer names
both. A request that refers to a UID window (`uid_from` or `uid_to`) must repeat the `uidvalidity` of the
earlier answer; if the folder's current `UIDVALIDITY` differs, the request is refused as invalid and nothing
is listed, because the old UIDs may now belong to other messages. `uidvalidity` without a UID is refused. When
both ends of the window are given, it spans at most 10000 UIDs; a window with only `uid_from` runs to the last
message, and the result count is still capped by `limit`.

## Reading a message

`infomaniak.mail.messages.get` takes `folder`, `uid`, and `uidvalidity`, all required; the UID and UIDVALIDITY
come from `messages.list`, and a `uidvalidity` that no longer matches the folder is refused as an invalid
request. It answers the envelope fields of `messages.list` plus the text and the attachment list. `message_id`
is the `Message-ID` header when it is well formed, the value for `in_reply_to` of `messages.send`.

- The text is the first `text/plain` part that is no attachment; without one, the first `text/html` part,
  returned as source. HTML is never rendered, and nothing referenced by it is fetched.
- The transfer encoding (base64, quoted-printable, 7bit, 8bit) is decoded, and so is the character set when it
  is UTF-8, US-ASCII, ISO-8859-1, ISO-8859-15, or Windows-1252. Any other set is read as UTF-8, where invalid
  bytes become U+FFFD instead of being guessed.
- At most 96 KiB of the part are fetched, as a byte range, and the decoded text is cut at 20000 characters.
  Control characters other than line breaks and tabs are replaced by a space.
- An attachment is every part other than the chosen text that is marked as an attachment, has a file name, or is
  no plain or HTML text; the other plain or HTML parts are alternatives of the text. `part` is the part number
  of the message's own BODYSTRUCTURE, such as `2` or `1.2`. Names have RFC 2047 words decoded and are
  cleaned and capped at 255 characters, types at 100. `size` is the part as stored in the message, in its
  transfer encoding, so a base64 attachment is about a third larger than the file.
- Each attachment also carries what the extended BODYSTRUCTURE says about it, from the `FETCH` that already
  reads the structure; the descriptor lists the members. All of it is untrusted text, cleaned and bounded in
  bytes. `parameters` (the Content-Type parameters) is all or nothing: beyond its bounds it is left out and
  `parameters_truncated` is true, never cut.
- `location` is the Content-Location as text and is never fetched. An address with user information, a query,
  or a fragment can carry an access token and is left out, as is one that cannot be parsed.
- The MD5 checksum of a part (`body-fld-md5`) is not returned: the IMAP library reads it but does not pass it
  on.
- A `message/rfc822` attachment is one entry; its nested structure is not evaluated.

## Reading an attachment

`infomaniak.mail.attachments.get` takes `folder`, `uid`, `uidvalidity`, and `part`, and optionally `local_path`.
The part number is only matched against the attachments of the message's own BODYSTRUCTURE; the section that is
fetched always comes from that structure, never from the argument, so `HEADER`, `TEXT`, or the message's text
cannot be asked for. A part that is no attachment is `not-found`.

- Without `local_path`, the decoded content is returned as `content_base64` with `size` (decoded bytes) and
  `sha256`, up to 4 MiB decoded. A larger attachment is refused as an invalid request that points to
  `local_path`; no more than 6 MiB of the transfer-encoded part are ever read for an inline answer.
- With `local_path`, an absolute path or one starting with `~/` in an existing directory the connection
  releases under `files.write`, the content is streamed into a temporary file that only becomes the target
  once it is complete. The answer carries `part`, `name`, `type`, `size`, and `sha256`, never the content.
  A path outside the release is refused before any secret is read or connection is opened, without naming
  the path. An existing file is kept unless the request is confirmed, and a failed or corrupt transfer leaves
  no file. The operation may run for up to 30 minutes instead of 30 seconds, as far as the caller's own
  timeout allows.
- Both modes read the content with `BODY.PEEK[part]` and decode base64 and quoted-printable; an encoding that is
  not defined, or content that does not decode, is an `invalid-provider-response`.
