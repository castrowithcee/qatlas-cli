---
description: >
  Describes SeaTable row operations, fixed base and table binding, connection permissions, and token limits.
type: knowledge
edit: shared
created: 2026-09-12
updated: 2026-09-12
---

# SeaTable

A credential is one API token for one base; a connection additionally fixes one table and optionally one
view. It can list and read rows (`read`) and create, update, or delete rows with the matching permission.
Mutations always address the fixed table, require confirmation, reject system-column names, and are bounded
to 1 MiB. A view narrows listing but does not redirect mutations to another table.

The credential provides `api-token`. Use provider permission `r` for read-only connections and `rw` when
writes are intended. The local `permissions` list is a separate ceiling and cannot turn an `r` token into a
writer. Qatlas exchanges the API token for a short-lived base token in memory.
