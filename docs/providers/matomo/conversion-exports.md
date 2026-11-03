---
description: >
  Describes the Matomo conversion export tools: reading and deleting the advertising conversion exports, the website targets, the rights, the plugin requirement, the fields that are never returned, and the limits of what the AdvertisingConversionExport reference proves.
type: knowledge
edit: shared
created: 2026-11-03
updated: 2026-11-03
---

# Matomo conversion exports

The conversion export tools of the [Matomo provider](README.md), for the premium plugin AdvertisingConversionExport.
An instance without the plugin refuses both tools as an unsupported capability, after one plugin check and without
further requests. They are in no [setup profile](README.md#setup-profiles); `matomo.conversion-exports.delete` is
offered only via a connection's `tools` list.

## Targets

Both tools are website tools. `idSite` must lie inside the `site` targets, otherwise the call is refused before any
secret is read and any request, without naming the website. An export ID counts across websites, so an export is
bound to its website by an answer of Matomo before it is shown or deleted:

- `list` without `idExport` asks for the exports of every website, or of `idSite` when given, and returns only
  the rows whose website lies inside the targets. A row without a readable ID or website is never shown.
- `list` with `idExport` reads that export. Its website must lie inside the targets and, when `idSite` is given,
  equal it; otherwise it is not found and nothing is shown.
- `delete` reads the export once first with the same check and then sends exactly one deletion. An export of
  another website is not found and nothing is deleted.

## Tools

| Tool | Does |
| --- | --- |
| `matomo.conversion-exports.list` | lists the conversion exports, or with `idExport` reads one |
| `matomo.conversion-exports.delete` | deletes a conversion export of a website (idempotent in effect, listed-only) |

- Names are untrusted and shortened.
- `delete` needs `confirm`, sends one request, and never repeats it: after an unclear result (timeout, reset
  connection, server error, unreadable answer) the error says so and points to `list` with `idExport` to read the
  export before trying again.

## Access tokens

An export is expected to carry an access token and an export address that includes it. The tools return neither. `list`
returns only an allowlist of fields (ID, website, name, type, and whether it is switched on); everything else of
the answer is dropped, and a name or type that contains a token is dropped as well. Token-like values (members named
like a token, secret, or key, export addresses, and the token parameters in them) are registered with the redactor
before the answer is read, so they do not appear in a message either.

Creating and changing exports and renewing the access token (`addConversionExport`, `updateConversionExport`,
`regenerateAccessToken`) are not offered: they create a token and with it a way for data to leave Matomo.

## Limits

- The forms of the answers and the names of the token members are not verified against a reference or an
  instance; the tool reads the Matomo core convention (an `idexport` and an `idsite` per export) and drops what it
  cannot attribute. The right is write on at least one website for `getConversionExports`, as the public demo
  states; for `delete` it is expected, not stated.
- From plugin 6.0.1 a Super User can switch the export off for the whole instance.
- The signatures come from the Matomo 6 reference. They are not verified against Matomo 5 (plugin 5.x); no version
  is checked per method, and an instance that lacks one ends as an unsupported capability.
- The plugin source is not public.
