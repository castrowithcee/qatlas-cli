---
description: >
  Describes the Penpot tools that list the libraries a file uses, share a file as a library, and link a file to a
  library, with their binding rules.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Penpot: libraries

Targets, binding, and the rules every change follows are in the [README](README.md#scope).

- `libraries.list` binds the file through its project and reads the libraries the file uses. Only libraries whose
  team is bound and whose project passes the allow-list are described; every other library is only counted in
  `outside_targets`, never named. At most 200 libraries are returned.
- `libraries.share`: **Sharing makes the file visible as a library to the whole team that owns it**, not only to this
  connection's projects. Unsharing removes the links of other files and copies the library's assets into them. The
  file is bound through its project first.
- `libraries.link`: `project_id` and `file_id` name the file that uses the library, `library_project_id` and
  `library_id` the library file. Both files are bound through their projects, which must pass the allow-list and lie
  in bound teams, so a link is only made between bound files. Penpot itself refuses a link to the file itself, across
  teams, and a circular link.

`share` and `link` follow [Changes](README.md#changes); read the libraries before trying again.
