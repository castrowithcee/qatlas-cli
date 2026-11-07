---
description: >
  Guides a user through setting up the Google Maps provider: creating an API key in a Google Cloud project with Places API (New) enabled and a billing account, adding the key to Qatlas, and what the connection test proves.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Set up Google Maps

This page shows how to connect Qatlas to Google Maps Platform. Fields, cost, and errors are described in the
[Google Maps provider](README.md); the tool is described in [places](places.md).

## What you need

- A Google Cloud project with a billing account. Google bills Places requests to that account; see
  [Cost](README.md#cost) for the free monthly caps.
- Places API (New) enabled in that project, and Geocoding API for the geocoding tools.
- An API key of that project.
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.
- The service is always `https://places.googleapis.com`; there is nothing to change for it.

## Create a key

In the Google Cloud console, choose or create the project and link a billing account to it. Under APIs and
services, enable **Places API (New)** and, for the geocoding tools, **Geocoding API**. Under Credentials, create an API key. Google changes its pages from
time to time; look for these names.

Restrict the key to the APIs you enabled. A leaked key is not narrowed by the connection's `tools`, so keep it
for Qatlas only and restrict it as far as your use allows.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qconnect tui`.
2. In the Connections area, press `n` and follow the steps: provider `google.maps`, the service, a
   credential, and the access.
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api_key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$GOOGLE_MAPS_API_KEY" | qconnect credential set google-maps-key api_key
```

`google-maps-key` is the name of the credential in your configuration and `api_key` is its only secret. To
replace a key, run the same command again. Run `qconnect config validate` to check the configuration file. It
contacts no provider.

```yaml
services:
  maps:
    provider: google.maps

credentials:
  google-maps-key:
    provider: google.maps
    type: keyring

connections:
  places:
    service: maps
    credential: google-maps-key
```

## Check the connection

In `qconnect tui`, open Connections, select the connection, and press `t`. `[ok]` means Google accepted the key
for Places API (New) and the project may call it. Qatlas reads one fixed place with the field mask `id`, which
Google documents as free.

**A passing test does not mean every Maps API works.** It does not test Routes, Geocoding, or any other API,
and it does not show the restrictions of the key for them.

| Result | Meaning |
| --- | --- |
| `auth` | Google does not accept the key; check the key |
| `permission` | Google refused the key for Places API (New); check that the API is enabled in the key's project, that the project has a billing account, and the key's restrictions |
| `rate-limited` | The key or its quota is used up |
| `not-found` | The test place is not found; its ID may have become obsolete |
| `invalid-provider-response` | The answer was not usable |
