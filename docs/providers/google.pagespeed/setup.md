---
description: >
  Guides a user through setting up the Google PageSpeed provider: creating an API key in a Google Cloud project with the PageSpeed Insights API and the Chrome UX Report API enabled, adding the key to Qatlas, and what the connection test proves and does not prove.
type: knowledge
edit: shared
created: 2026-11-06
updated: 2026-11-06
---

# Set up Google PageSpeed

This page shows how to connect Qatlas to PageSpeed Insights and the Chrome UX Report. Results, bounds, and
errors are described in the [Google PageSpeed provider](README.md); the tools are described in
[tools](tools.md).

## What you need

- A Google Cloud project.
- The PageSpeed Insights API enabled in that project.
- The Chrome UX Report API enabled in the same project, for field data. `crux.query-record` and
  `crux.query-history-record` need it, and so does the connection test.
- An API key of that project.
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.
- The service origins are fixed; there is nothing to change for them.

## Create a key

In the Google Cloud console, choose or create the project. Under APIs and services, enable **PageSpeed
Insights API** and **Chrome UX Report API**. Under Credentials, create an API key. Google changes its pages
from time to time; look for these names.

Restrict the key to those two APIs. A leaked key is not narrowed by the connection's `tools`, so keep it for
Qatlas only and restrict it as far as your use allows. Neither service takes the key in a header, so it travels
in the query of each request; Qatlas keeps that URL out of every message and log.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qconnect tui`.
2. In the Connections area, press `n` and follow the steps: provider `google.pagespeed`, the service, a
   credential, and the access.
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api_key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not
appear in the shell history:

```sh
printf %s "$GOOGLE_PAGESPEED_API_KEY" | qconnect credential set google-pagespeed-key api_key
```

`google-pagespeed-key` is the name of the credential in your configuration and `api_key` is its only secret.
To replace a key, run the same command again. Run `qconnect config validate` to check the configuration file.
It contacts no provider.

```yaml
services:
  pagespeed:
    provider: google.pagespeed

credentials:
  google-pagespeed-key:
    provider: google.pagespeed
    type: keyring

connections:
  analysis:
    service: pagespeed
    credential: google-pagespeed-key
```

## Check the connection

In `qconnect tui`, open Connections, select the connection, and press `t`. `[ok]` means Google accepted the
key and the Chrome UX Report API is enabled in its project. Qatlas asks that API for one metric of one fixed
origin; an answer without data for that origin counts as a success.

**A passing test does not mean PageSpeed Insights works.** It does not test the PageSpeed Insights API, so it
does not show that this API is enabled in the project or what is left of its daily quota. Run
`google.pagespeed.pagespeed.run` once to check it.

| Result | Meaning |
| --- | --- |
| `auth` | Google does not accept the key; check the key |
| `permission` | Google refused the key; check that both APIs are enabled in the key's project and the key's restrictions |
| `rate-limited` | The key or its quota is used up |
| `invalid-provider-response` | The answer was not usable |
