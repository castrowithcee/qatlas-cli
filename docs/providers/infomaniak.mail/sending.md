---
description: >
  Describes the Infomaniak Mail sending tools (messages.send, drafts.send): the fixed SMTP endpoint, sender
  binding, recipients and Bcc, replies, attachments, the once-only rule, the Sent folder copy, the checks on a
  draft to send, risk and tools list requirement, and the test approach.
type: knowledge
edit: shared
created: 2026-10-01
updated: 2026-10-22
---

# Sending

The sending tools of [Infomaniak Mail](README.md).

## Sending

`messages.send` sends a new message or a reply; `drafts.send` sends an existing draft. Both submit the message
over SMTP and then store a copy in the Sent folder.

| Tool | Arguments | Answer |
| --- | --- | --- |
| `messages.send` | the fields of a draft (`to`, `cc`, `bcc`, `subject`, `body`, `attachments`) plus `in_reply_to` and `references` | `message_id`, `recipients`, `size`, `copy_stored`, `sent_folder`, `sent_uidvalidity`, `sent_uid`, `note`, and per attachment `name`, `type`, `size`, `sha256` |
| `drafts.send` | `folder`, `uid`, `uidvalidity` of the draft | the same, with `draft_kept: true` |

- **Endpoint.** SMTP goes only to `mail.infomaniak.com:587`. The client reads the greeting, says EHLO, and
  requires `STARTTLS`; the certificate and host name are verified like the IMAP ones (TLS 1.2 or newer). A server
  that does not offer STARTTLS, or a handshake that fails, ends the call **before** `AUTH`, so no credential
  and no message leaves in plain text. `AUTH PLAIN` (or `AUTH LOGIN` when PLAIN is not offered) follows only
  inside TLS, with the mailbox address and the same mailbox password as the IMAP login; the password stays in
  the redactor and never appears in an error. No server text is copied into an error.
- **Sender binding.** `From` is the mailbox of the connection and nothing else, and it is also the envelope
  sender (`MAIL FROM`). There is no `from` argument. A connection with sender targets must list its own
  mailbox, otherwise nothing is sent (the narrower reading: the mailbox is the only sender, so the list can
  only allow or forbid sending as a whole). `drafts.send` additionally refuses a draft whose `From` is not that
  mailbox.
- **Recipients and Bcc.** The envelope recipients are the typed `to`, `cc`, and `bcc` only, at most 50 together,
  plain addresses without display names, each once (compared case-insensitively). `Bcc` is never written into
  the sent message: those addresses are only `RCPT TO`. The stored copy is the sent message and therefore has
  no `Bcc` header either (the copy does not record the Bcc recipients; this is the narrower reading).
  No envelope address, host, or header comes from an argument other than these fields.
- **Replies.** `in_reply_to` is one Message-ID `<id@host>`; `references` is a list of up to 20 such IDs, oldest
  first, and needs `in_reply_to`; without it `References` is `in_reply_to` alone. A Message-ID is validated
  strictly (angle brackets, one at sign, letters, digits, and a few punctuation characters, at most 403
  characters; no space or control character). Replies are given by Message-ID, not by a reference to a stored
  message: that needs no extra IMAP read and no access to a folder or sender; the ID comes from the
  `message_id` of `messages.get`. The reply fields exist only on `messages.send`; a draft carries them as
  headers.
- **Attachments.** As for [drafts](drafts.md): `local_path` under `files.read` or `content_base64` up to 4 MiB, at most 10,
  15 MiB together. The answer holds metadata only; never the text or an attachment's content. A message above
  1 MiB may take up to 30 minutes instead of 30 seconds.
- **Once only.** A call runs exactly one SMTP transaction with one `DATA` transfer and never repeats it, not on
  another connection either. A failure before the end of DATA (connection, STARTTLS, AUTH, sender, a recipient,
  the DATA command, or while the text is written) means the message was not sent; the error says so, and one
  refused recipient refuses the whole message. After the terminating dot, a reply with a 4xx or 5xx code (except
  421) means the server declined the message and the error says it was not sent. Every other outcome there (a
  timeout, a dropped connection, an unreadable answer, a 421) is unknown: the `timeout` or `unreachable` error says that the
  message may have been sent and must be checked in the Sent folder and with the recipients; nothing is
  repeated and no copy is stored. A message is also not marked or deduplicated afterwards, so a deliberate second
  call sends again and needs its own confirmation.
- **Sent folder.** The copy is stored with one `APPEND` and the flag `\Seen` in the one folder the server marks
  with SPECIAL-USE `\Sent`, found like the [trash](changes.md#changing-a-message) and [drafts](drafts.md) folders:
  never by name, one only, inside the folder targets. The folder is determined **before** anything is sent; if it
  cannot be identified, the call is refused
  as an invalid request and nothing is sent (the narrower of the two readings: no message without a copy).
  If the `APPEND` fails after a successful submission, the message is still sent: the call returns a result,
  not an error, with `copy_stored: false` and a `note` that says the message was sent, the copy may be missing, and
  it must not be sent again. Nothing is repeated. Whether Infomaniak stores a copy of mail submitted over SMTP by
  itself, which would make the `APPEND` redundant, is not settled and is checked in the live test.
- **drafts.send.** The draft must be in the drafts folder (the one `\Drafts` folder) and carry `\Draft`,
  otherwise `not-found` and nothing happens. It is read with `BODY.PEEK` and never sent raw. Only a draft in the
  structure Qatlas writes is accepted: exactly the headers `From`, `To`, `Cc`, `Bcc`, `Subject`, `Date`,
  `Message-ID`, `In-Reply-To`, `References`, `MIME-Version`, `Content-Type`, and `Content-Transfer-Encoding`, each
  at most once and without control characters; plain addresses; one `text/plain` part, alone or in a
  one-level `multipart/mixed` with base64 attachments; a 7-bit body; at most 10 attachments, 15 MiB together;
  at most 50 recipients; at most 25 MiB. Anything else (a foreign header such as `X-Mailer` or `Reply-To`, HTML,
  a nested or 8-bit message, a display name) is refused as an invalid request, and a draft from another
  mail client usually is. The headers are written again from the checked values, `Bcc` is removed, `Date` is set to
  the time of sending, and the body is kept byte for byte. The draft is **not** deleted or changed after
  sending and stays in the drafts folder (the narrower reading); remove it with `drafts.delete` if it is no longer needed.
- **Risk and tools list.** Both are `create`, non-idempotent, confirmed, open-world, class
  `infomaniak-mail-messages`, and require the connection's tools list; neither is in any profile. A call
  without `confirm` does nothing. Mail leaves the mailbox for third parties and cannot be recalled.
- **No test reaches Infomaniak.** The tests replace the single seam `dialSMTP` and talk to an in-test SMTP
  server on a loopback port; they also check the order STARTTLS, AUTH, MAIL, RCPT, DATA, the credential inside
  TLS only, the missing Bcc header, one DATA per call, and that no live connection is made.

```yaml
connections:
  customer-a-send:
    service: infomaniak-mail
    credential: customer-a-mail
    targets: [mailbox/office@example.com, folder/INBOX, folder/Drafts, folder/Sent]
    files:
      read: [~/uploads]
    tools: [infomaniak.mail.messages.get, infomaniak.mail.drafts.send, infomaniak.mail.messages.send]
```
