---
description: >
  Describes Nextcloud administration tools and their safety boundaries.
type: knowledge
edit: shared
created: 2026-10-23
updated: 2026-10-26
---

# Nextcloud administration

Group `admin`: read-only administration of users and groups.

`nextcloud.admin.users.*` and `nextcloud.admin.groups.*` read users, groups, and members and need a tools list (the
role is under [Administration](setup.md#administration)); an `admin` connection test reads one user page. Users are
never created or invited, and no change is offered: Nextcloud demands a password confirmation an app password
cannot give.
