---
description: >
  Describes the instance-wide tools shared by the Gitea and Forgejo providers: the settings, signing keys, NodeInfo,
  and the rendering of Markdown and markup, with bounds, and product differences.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-26
---

# Instance-wide tools

`settings.get` (argument `section`: `api`, `repository`, `attachment`, or `ui`), `version.get`, `signing-keys.get`,
and `nodeinfo.get` (Forgejo only) read data of the instance, not of an owner or repository. Every connection offers
them, whatever its targets.

`markdown.render` and `markup.render` send a text to the instance and return HTML. They change nothing and carry
the `read` effect. Without `repository` the text renders with no repository, which touches no customer data. With
`repository` (`OWNER/REPO`, checked against the targets like any repository argument) the instance resolves links
and references against that repository. The request body holds only the documented members, built from validated
arguments: the text, the mode, the repository reference, and the members a product documents. `markdown.render`
with `raw` posts the text alone to the raw endpoint. The form of the repository reference differs by product and
was not tried against a live instance; an instance that rejects it answers `provider-error`.

`signing-keys.get` returns the public key the instance signs commits with, not an access credential. The format is
`gpg` for both products; the SSH public key is `pub` on Gitea and `ssh` on Forgejo. The other product's format is
refused before the token is read.

`max_response_items` and `default_paging_num` from `settings.get` describe the instance; paging still follows
[Pagination](README.md#pagination).

A rendered text is accepted up to 256 KiB and returns at most 1 MiB of HTML, with `truncated` set when it was cut;
the HTML is never executed or followed. NodeInfo `metadata` is free-form and bounded in depth, length, and key count.

| Tool | Effect | Provider |
| --- | --- | --- |
| `settings.get`, `signing-keys.get` | read | both |
| `markdown.render`, `markup.render` | read | both |
| `nodeinfo.get` | read | Forgejo |

## Gitea

- `settings.get` with `api` also carries `default_max_response_size`.
- The SSH signing key is `signing-keys.get` with `format` `pub`.
- The render modes are `markdown`, `comment`, `wiki`, and `file`, and the repository reference is the URL path
  `/OWNER/REPO`. The `Wiki` flag of Gitea's render requests is deprecated and not offered; `mode` `wiki` replaces it,
  and there is no `branch_path`.
- Gitea has no `nodeinfo.get`.
