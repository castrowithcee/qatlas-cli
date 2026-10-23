---
description: >
  Describes Nextcloud CardDAV tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud contacts

Group `contacts`: address books and contacts.

The group `contacts` (profile `contacts`) reads address books and contacts (untrusted personal data) under the contact
[limits](../infomaniak.dav.md#contacts); the get never requests photo, logo, sound, key, or agent data, as it would come
embedded. The system address book needs its own `addressbook/URI` target and is never written or shared. Writing is in
no profile; deleting and sharing need a tools list. A contact update replaces by `etag`, refusing a card it cannot write
whole. A new address book needs the general `addressbook` target; changing, deleting (with all contacts), or sharing one
needs it bound and owned. Sharing: one user or group, never link, e-mail, or team; unknown ones are ignored (`shares`).
