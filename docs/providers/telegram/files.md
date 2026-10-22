---
description: >
  Describes how Telegram files are read and downloaded through file references.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Files

`telegram.files.get` (`read`) returns the size and `file_unique_id` of one file; `telegram.files.download` (`read`)
writes it to `local_path`. Both take only a `file_ref` from `media.file_ref` of
[`telegram.updates.list`](updates.md): a raw file identifier and a reference of another target, token, or kind are
refused.
`telegram.files.download` replaces an existing file only with confirmation, and a failed or incomplete transfer
leaves no file. The result carries size and SHA-256, never the content. Files above 20 MB are refused before the
download, and a longer response than reported is rejected. The download URL carries the bot token; it and Telegram's
`file_path` appear in no output, error, or log, and a redirect is never followed.
