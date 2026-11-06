---
description: >
  Describes the Google Maps place tool: places.get reads the details of one place by place ID with a required field mask, the billing group of the mask, the arguments and their forms, and the bounded output.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Google Maps places

The place tool of the [Google Maps provider](README.md). Configuration, the field groups and their cost,
attribution, rate spacing, errors, and the boundary are described there.

## google.maps.places.get

`GET /v1/places/{place_id}`. It returns the fields of one place that `field_mask` names. Effect `read`.

- `place_id` is required: 1 to 256 letters, digits, `-`, and `_`. A `places/` resource name, a `/`, `..`, or
  any other character is refused before a request, so an argument can never add a path segment or a query.
- `field_mask` is required, a list of field names or one comma-separated string, such as
  `["id","displayName"]`. There is no default and no wildcard; see
  [Field mask and billing group](README.md#field-mask-and-billing-group). A repeated name is sent once. The
  highest group of the mask decides the billed SKU, so a mask of `id` only is free and `reviews` is the most
  expensive group.
- `language_code` is a language tag such as `en` or `pt-BR`.
- `region_code` is two letters such as `US`; Google takes no 3-digit region.
- `session_token` is at most 36 letters, digits, `-`, and `_`. It only matters for the billing of a lookup
  that ends an Autocomplete session of the same project.

The result has Google's own member names, `id`, `displayName.text`, `location.latitude`, and so on, and only
the members the mask names and Google has a value for. A member that Google does not document for a place is
ignored. Texts are untrusted; links are text and are never fetched. Reviews are cut to 5 and photos to 10, each
with its author attribution. A photo shows its resource name, not its media.

Show Google Maps attribution wherever the content appears, and show `attributions` with the result.
