---
description: >
  Describes the Penpot tools that read teams, projects, and files and the limits of `files.get`, the tools that
  create, rename, move, and delete projects and files, and the tools for snapshots and deleted files.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Penpot: projects and files

Targets, binding, and the rules every change follows are in the [README](README.md#scope).

## Reading

The list tools describe what Penpot returns for their commands. `projects.list` also lists a deleted project that
Penpot still lets a person restore, since Penpot 2.12; it carries `deleted_at`, and the target binding still goes
through the project list, deleted projects included. Penpot returns only files and libraries that are not deleted
there; the `deleted-at` field would always be empty and is not returned.

`files.get` reads a file's library summary (for components, variants, colors, and typographies a `count` and up to 20
`samples`) and one page: its metadata and up to 200 shapes. `page_id` is optional; the first page of the file is read
when omitted. It reads the project's file list and refuses a `file_id` that is not in it before the summary or page is
requested. Shapes are listed in ID order. A shape carries the attributes Penpot's schema defines for its type
(geometry, styling, component references, layout, interactions, and content); the descriptor lists them. Keys are
snake case, whether Penpot sends kebab case or camel case; map keys that are IDs and the keys of `plugin_data` stay
as they are. Missing and empty values are left out, numbers are passed on as written.

The answer is bounded: free values go to depth 8, 200 entries per list or object, and 4 KiB per text, and the
attributes of all shapes together take 1 MiB, after which `truncated` is set. A value beyond a bound is left out whole,
never cut, and named in `omitted_fields` of its shape (or of the page). Binary-encoded shape content is not returned
and is named the same way. Identifiers that point to libraries outside the targets, such as `component_file`,
`shape_ref`, and media in fills, are returned as identifiers only; they are neither resolved nor requested, so the
call makes the same two requests after the file binding as before. Names, text content, and plugin data are untrusted
provider data; control characters in them become spaces. A file's page list is not offered: the summary carries none,
so other pages are reached only through a known `page_id`.

## Projects and files

The management tools take IDs from the list tools and change one thing with one fixed command. Names have 1 to 250
characters (Penpot's own limit), are not blank, and carry no control characters; an answer never repeats a name, only
IDs.

- `projects.create`: the team must be a bound team. **A connection with a project or file allow-list refuses it**,
  because a new project could not be inside that list.
- `projects.rename` and `projects.delete`: the project must pass the allow-list and lie in a bound team (proved with
  `get-projects`). Penpot marks a deleted project and removes it and its files after its deletion delay, and refuses
  the default project of a team. A project that is in the allow-list may be deleted; the boundary is not widened by
  that.
- `files.create`: the project is bound as above.
- `files.rename`: the file is bound through its project (project located, file found in its file list) before
  anything is sent.
- `files.move`: `project_id` is where the file is now. Both projects must pass the allow-list and lie in bound teams,
  and they must differ; the file is bound through the source project. A move between two bound teams is allowed when
  Penpot allows it. Penpot drops library links of the moved file that would cross teams.

For a creation, an answer without the new ID also counts as unreadable ([Changes](README.md#changes)).

## Snapshots and deleted files

The five tools use the read commands `get-file-snapshots` and `get-team-deleted-files` for their checks; none of them
is offered as a tool, so the IDs of older snapshots and deleted files come from the Penpot UI or from the answers of
earlier calls.

- `snapshots.create`: `label` is optional (1 to 250 characters; Penpot generates one when omitted). The file is bound
  through its project. Penpot limits snapshots per file and team and refuses beyond that. The label is not repeated
  in the answer.
- `snapshots.restore`: **It overwrites the current content of the file**; Penpot keeps a temporary backup snapshot of
  the state before. The snapshot ID must appear in the visible snapshots of the bound file (`get-file-snapshots`),
  else the call is refused before anything is changed.
- `files.delete` (file bound through its project) soft-deletes the file: Penpot marks it, removes the library links
  that point to it, and removes it after its deletion delay.
- `files.restore` and `files.purge` take the `project_id` and `file_id` of a file that is no longer in its project's
  file list. The file is bound through `get-team-deleted-files` of each bound team: it must appear there and report
  the given project, which must pass the allow-list. The team ID is taken from that binding; it is never an argument.
  A file that is not deleted, or that lies in another project, is refused without naming it. Exactly one file is
  sent. `files.restore` also removes the deletion mark of the file's project, as Penpot does. **`files.purge` deletes
  the file for good, before its deletion delay ends, and cannot be undone.**

Both commands answer with a server-sent event stream; the change counts as done only when its final event names the
file. An error event, or an answer that does not name the file, is reported as a failure; a stream that ends without
a final event is reported as uncertain, and the error then says that the change may have taken effect.
