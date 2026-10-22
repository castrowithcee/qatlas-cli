---
description: >
  Describes the Penpot tools that add images to a file, export a file as a `.penpot` archive, and import an archive
  as a new file: local paths, URL checks, limits, and timeouts.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Penpot: media, export, and import

These four tools move content between Penpot and local files. Local files go through the directories the connection
releases (`files.read` for reading, `files.write` for writing); a path outside them is refused before the credential is
resolved and before any request is sent, and the refusal never names the path. Answers carry IDs and metadata only
(size, SHA-256), never file content. Targets and binding are in the [README](README.md#scope).

- `media.upload` takes either `local_path` or `content_base64` (up to 4 MiB; with a `name`, whose extension names the
  image type). Types: png, jpeg, gif, webp, svg, chosen by the file name extension. Images are limited to 64 MiB by
  Qatlas and by the instance's own limit. The file is bound through its project.
- `media.from-url`: **Penpot itself fetches the URL.** Qatlas accepts only an `https` URL of up to 2048 characters on
  the default port, without user info or fragment, whose host is a DNS name of at least two labels with an alphabetic
  last label. Every IP form (also decimal, octal, hexadecimal, or IPv6), single-label names, and the suffixes
  `localhost`, `local`, `internal`, `intranet`, `lan`, `home`, `corp`, `localdomain`, `home.arpa`, and `private` are
  refused before any secret or request. This check is syntactic: Qatlas does not resolve the name and cannot see where
  Penpot's DNS lookup or its (up to three) redirects lead, so the instance's own network controls remain the real
  boundary. The URL is not repeated in the answer.
- `files.export` writes the file as a `.penpot` archive. It reads and changes nothing in Penpot except a temporary
  copy that Penpot keeps for about an hour. **Libraries are not included and their assets are not embedded**
  (`include-libraries` and `embed-assets` are always `false`), since library files may lie outside the connection's
  projects. An existing local file is replaced only when `confirm` is set; the archive is written to a temporary file
  first and made visible only when complete. Limit: 1 GiB.
- `files.import` creates a new file in the project from a `.penpot` archive (the project is located in the bound
  teams). Penpot's import has no overwrite parameter, so it never replaces a file, which is why the tool needs no tool
  allow-list. The archive is sent in one request, up to 512 MiB and the instance's own upload limit; chunked uploads
  are not used. Penpot checks the archive. `file_ids` lists up to 20 IDs, when Penpot reports them.

`media.upload`, `media.from-url`, and `files.import` follow [Changes](README.md#changes); an import stream without a
final event also counts as uncertain, and the file or project is read before trying again. Transfers use a 30 minute
timeout instead of the 30 seconds of the other requests (`media.from-url`: 2 minutes). The export's archive is
fetched from `/assets/by-id/<id>` on the connection's own origin, never from a host named in an answer, and without the
token.
