---
description: >
  Describes the Penpot comment tools that list, create, update, and delete threads and comments of a file: data
  handling, the two forms of `comments.create` and `comments.update`, and ownership.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Penpot: comments

The comment tools take `project_id` and `file_id`, bind the file through its project ([README](README.md#scope)), and
work on the threads and comments of that file. Comment texts and the people behind them are personal data: the tools
have their own sensitivity class, `penpot-comments`. The reading tools give out the author details Penpot sends
(name, email address, profile image ID) as untrusted personal data; a thread whose team or project lies outside the
connection's targets keeps identifiers only. Refusals, errors, and logs never carry them, and no comment text is
ever echoed after a change.

- `comments.threads` lists the file's threads with an `excerpt` of the first comment (up to 750 bytes).
- `comments.list` lists the comments of a `thread_id`, oldest first, with `content` of up to 3000 bytes.
- `comments.create` makes the form by its arguments. With `thread_id` it adds a comment to that thread
  (`create-comment`); without it, `page_id`, `frame_id`, and `position` (`x`, `y`) start a new thread
  (`create-comment-thread`), after the page has been read to prove that the frame lies on a page of the file. Mixing
  the two forms is refused. The text has 1 to 750 characters (Penpot's own limit). Qatlas sends no mentions; Penpot
  may still notify members of the team by email, according to their notification settings.
- `comments.update` with `comment_id` and `content` edits a comment (`update-comment`); with `is_resolved` and
  without `comment_id` it resolves or reopens the thread (`update-comment-thread`).
- `comments.delete` with `comment_id` deletes that comment (`delete-comment`); without it, the whole thread with all
  its comments (`delete-comment-thread`). The deletion is final.

A thread or comment ID is checked against the bound file before it is read, updated, or deleted: the thread must
appear in the file's threads (`get-comment-threads`) and a comment in the thread's comments (`get-comments`). These
reads are the only I/O before a refusal; the refusal does not name the ID. Threads and comments that report another
file or thread are dropped.

The rights for the comment tools, including editing and deleting only your own comments, are listed under
[Rights per tool group](setup.md#rights-per-tool-group). A thread that has no comment is not listed by Penpot and
cannot be addressed.

Changes follow [Changes](README.md#changes); read the comments of the file before trying again.
