---
description: >
  Describes Nextcloud administration tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-23
---

# Nextcloud administration

Group `admin`: read-only administration of users and groups.

`nextcloud.admin.users.*` and `nextcloud.admin.groups.*` read users, groups, and members as an administrator or as a
subadmin within its groups and need a tools list; an `admin` connection test reads one user page. Users are never
created or invited, and no change is offered: Nextcloud demands a password confirmation an app password cannot give.
