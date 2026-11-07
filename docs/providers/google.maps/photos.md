---
description: >
  Describes the Google Maps photo tool: photos.get resolves the photo name of a place result to a short-lived image address, with its argument forms, the size limits, the Place Photos cost, author attribution, and why no image is ever loaded or a redirect followed.
type: knowledge
edit: shared
created: 2026-11-07
updated: 2026-11-07
---

# Google Maps photos

The photo tool of the [Google Maps provider](README.md). Configuration, rate spacing, errors, and the boundary
are described there.

## google.maps.photos.get

`GET /v1/{name}/media`. It resolves one photo of a place to a short-lived address of the image. Effect `read`;
the call is safe to repeat, though every call can be billed.

- `name` is required: the photo resource name `places/{place_id}/photos/{photo}` exactly as a result of
  `places.get`, `places.search-text`, or `places.search-nearby` returns it in `photos[].name`. Take it from a
  fresh result: Google states that a photo name cannot be cached and can expire. Qatlas accepts only those four
  segments, with letters, digits, `-`, and `_` in the two IDs (the photo reference up to 1024 characters).
  A scheme, a `/media` suffix, `..`, a query, a further segment, or any other path is refused before the key is
  read and without a request.
- `max_width_px` and `max_height_px` are each 1 to 4800 pixels, and at least one is required. Google scales the
  image down to fit the smaller of the two, keeping its aspect ratio, and never enlarges it. A missing size
  or a value outside the range is refused before a request.

Qatlas always sends `skipHttpRedirect=true`, so Google answers with JSON instead of the image; it is not an
argument. The result has `photoUri`, the short-lived address of the image, and `name`, the resource name of the
photo media (`.../media`). Both are untrusted third-party data and cut when long; a `photoUri` that is not an
absolute `http` or `https` address without user information is `invalid-provider-response`.

## No image is loaded

The provider reads only the JSON answer. It never loads image data, never fetches `photoUri`, and never follows
a redirect: the redirect client of this provider refuses every redirect, and a `3xx` answer is
`invalid-provider-response`, because the request asked for JSON. Fetching the address, if wanted, is a decision
of the caller outside this provider. The key is sent in the header only and is never part of the address.

## Cost

A call is billed as `Places Photo`, which Google's price list puts in the Enterprise group with 1,000 free
requests per month and billing account. Each call can be billed above that; check the price
list for current values. Google recommends loading photos on demand, because many photos of a place at once can
end in a `429`, which Qatlas maps to `rate-limited` and does not repeat.

## Attribution

Google Maps attribution is mandatory wherever Places content is shown. A photo also carries its own author
attribution: the `authorAttributions` of its entry in `photos` (`displayName`, `uri`, `photoUri`) from the place
result, which can be empty. Show it wherever the image is shown. Place IDs may be stored without limit; photo
names and the other caching rules are not checked here.
