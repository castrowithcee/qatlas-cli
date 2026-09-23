---
description: >
  Describes Nextcloud file operations, fixed Files roots, connection permissions, and WebDAV safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-23
---

# Nextcloud

A connection binds one identity to one fixed folder in the Files app. It lists and reads metadata or up to
4 MiB of file content (`read`), creates files only when absent (`create`), replaces an existing version with
an ETag precondition (`update`), and deletes only files with an ETag precondition (`delete`). It never offers
recursive folder deletion; parent folders must already exist.

Credentials provide `user-id` and a revocable `app-password`. Relative paths cannot escape the configured
root. Connection permissions independently hide and block operations, while the identity's WebDAV rights
remain the provider-side ceiling. An optional `tools` list narrows a connection further to named tools, for
example `[nextcloud.files.list, nextcloud.files.stat]` for metadata without file content, and never admits
an effect `permissions` excludes. File content is carried as base64 and never written to audit records. The
terminal editor starts a new connection on the setup profile `read`, which ticks `[read]` and
`[nextcloud.files.list, nextcloud.files.stat, nextcloud.files.get]`. A profile is a visible starting
selection, not a role: only the ticked `permissions` and `tools` are saved, every tick can be changed before
saving, and a saved connection never follows a profile.
