---
description: >
  Describes Nextcloud file operations, fixed Files roots, connection permissions, and WebDAV safety boundaries.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-12
---

# Nextcloud

A connection binds one identity to one fixed folder in the Files app. It lists and reads metadata or up to
4 MiB of file content (`read`), creates files only when absent (`create`), replaces an existing version with
an ETag precondition (`update`), and deletes only files with an ETag precondition (`delete`). It never offers
recursive folder deletion; parent folders must already exist.

Credentials provide `user-id` and a revocable `app-password`. Relative paths cannot escape the configured
root. Connection permissions independently hide and block operations, while the identity's WebDAV rights
remain the provider-side ceiling. File content is carried as base64 and never written to audit records.
