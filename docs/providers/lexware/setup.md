---
description: >
  Guides a user without special knowledge through setting up the Lexware Office provider: creating an API key, adding it to Qatlas, checking the connection, the rights and contract features each tool group needs, and fixing errors by symptom.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Set up Lexware Office

This page shows how to connect Qatlas to Lexware Office, which rights the API key and the contract need, and what to
do when Lexware refuses a request. Fields, tools, arguments, profiles, and targets are described in the
[Lexware Office provider](README.md); this page names only the rights.

**The API key decides what is possible at all.** The rights you set in Qatlas (the connection's `tools`, `effects`,
and target) limit it further. A call works only if both allow it. A connection can never do more than its key and
the Lexware contract allow, and a key never widens the connection.

## What you need

- A Lexware Office account whose organization Qatlas should work with. A key belongs to exactly one organization.
- A Lexware contract that includes the public API. Without it, Lexware refuses calls; see
  [Fixing errors](#fixing-errors).
- An API key. Lexware has no other credential for this provider: no password, no OAuth login. The key is sent as a
  bearer token.
- Qatlas keeps the key in the system keyring or the vault, or reads it from an environment variable.
- Qatlas reaches only the fixed Lexware gateway; the service address cannot be changed. See
  [Lexware Office](README.md).

## Create an API key

1. Sign in to Lexware Office and open <https://app.lexware.de/addons/public-api>. Lexware changes its pages from
   time to time; look for the page called **Public API**.
2. Create a new key and give it a name you will recognize, for example `qatlas-reader`.
3. If Lexware lets you choose the scopes of the key, pick only those for the areas you will use; see
   [Rights per tool group](#rights-per-tool-group). Lexware's API documentation mentions that scopes can be selected
   and that a user can hold several keys, but it names no scope per area, so follow the labels Lexware shows.
4. Copy the key at once. Lexware shows it only once.

Give a key only what a connection needs, and use a separate key for each Lexware organization.

## Add it to Qatlas

The guided setup does all of this for you:

1. Run `qatlas tui`.
2. In the Connections area, press `n` and follow the steps: provider `lexware`, the service, a credential, the
   optional organization target, and the access (by effect or by tools).
3. When the setup asks for the credential, choose where to keep it and paste the key as its `api-key` secret.

Without the guided setup, store the key on the command line. It is read from standard input, so it does not appear
in the shell history:

```sh
printf %s "$LEXWARE_API_KEY" | qatlas credential set lexware-reader api-key
```

`lexware-reader` is the name of the credential in your configuration and `api-key` is its only secret. To replace a
key that was revoked or regenerated, run the same command again with the new key. Run `qatlas config validate` to
check the configuration file. It contacts no provider.

A connection may name the organization its key must belong to; see the organization target in
[Lexware Office](README.md). Qatlas then refuses a key of another organization before the first request.

## Check the connection

In `qatlas tui`, open Connections, select the connection, and press `t`. `[ok]` means Lexware accepted the key for
reading one entry of the voucher list. With an organization target, the same test also fails if the key belongs to
another organization.

**A passing test does not mean every tool works.** Lexware checks the key's rights and the contract again on every
call. Compare the key and the contract with [Rights per tool group](#rights-per-tool-group) for the tools you use. A
failing test or call is explained under [Fixing errors](#fixing-errors).

## Rights per tool group

Lexware's API documentation names no scope per endpoint. It states that a refusal with HTTP 403 means "insufficient
scope or insufficient access rights in Lexware" and a refusal with HTTP 402 means a Lexware contract issue. So this
page cannot give a scope for every tool; it lists what Lexware documents. Rights for a change always include the
rights to read.

- **Key**: the scopes chosen when the key was created, and the access rights in Lexware of the user it belongs to.
  Where Lexware lets you choose scopes, take those that cover the area of the tools you use.
- **Contract**: the features of the Lexware contract. Lexware reports them as business features such as `INVOICING`
  and `INVOICING_PRO`; `lexware.profile.get` shows what your organization holds.
- Tools offered only when a connection's `tools` list names them (for example issuing, booking, and deleting) need
  the same rights as their group.

| Tool group | What Lexware documents |
| --- | --- |
| Quotations: read, draft, issue | A key created before Lexware added quotations to its API does not hold this access; create a new key |
| English invoices, invoices for foreign countries, XRechnung | Part of `INVOICING_PRO`, as far as Lexware documents it |
| Print layouts | The contract needs `INVOICING_PRO`; without it the call fails as a missing contract scope |
| All other tools: sales vouchers, recurring invoice templates, contacts, articles, voucher list, bookkeeping vouchers and files, organization profile and reference lists, webhook subscriptions | Lexware names no further right; a refusal tells you the key or the contract lacks one |

## What happens without a right

When the key or the contract lacks a right, the tool fails. The error says `permission` or `not-found`; see
[Fixing errors](#fixing-errors).

## Fixing errors

Every failed call has a **code** and a **message** from Qatlas.

### Reading a refusal

```text
permission: the API key is not permitted to perform this operation; check the rights of the API key in Lexware
```

- The message is **Qatlas' own**. It names what to check.
- No second part follows. Lexware echoes request details in its error answers, so Qatlas never copies them; the
  code and the HTTP status are what Qatlas reports.
- A change that cannot be confirmed (a timeout, a dropped connection, a server error) says that the voucher or object
  may have been created, issued, or changed. Check Lexware before you repeat it.

### By symptom

| You see | Cause | What to do |
| --- | --- | --- |
| `auth`: Lexware rejected the API key | The key is wrong, revoked, or disabled, or Lexware is briefly unavailable | Check <https://status.lexware.de>. Then create a new key ([Create an API key](#create-an-api-key)) and store it again with `qatlas credential set <credential> api-key`. |
| `auth`: the Lexware API key is unusable | The stored key holds a space, a line break, or other characters a key cannot have, or is too short | Store it again with `printf %s "$LEXWARE_API_KEY" \| qatlas credential set <credential> api-key`. |
| `permission`: the Lexware contract does not include this API access; check the contract in Lexware | The contract has no public API, or not the feature this tool needs | Check the contract in Lexware. Print layouts and the extended invoicing features need `INVOICING_PRO`, see [Rights per tool group](#rights-per-tool-group). |
| `permission`: the API key is not permitted to perform this operation; check the rights of the API key in Lexware | The key lacks the scope for this area, the Lexware user lacks the access right, or the key predates the area (quotations) | Create a new key with the scope for the area chosen, or give the Lexware user the access right, then store the key again. |
| `permission`: the API key belongs to a different organization than the one this connection is bound to | The key is for another organization than the connection's target | Use the key of the organization named in the target, or change the target. The message names neither organization. |
| `not-found`: Lexware does not hold this ... or does not show it to this API key | The identifier is wrong, **or** the key cannot see the object. Lexware answers both the same | Check the identifier. Then check that the key belongs to the right organization and holds the scope for the area. |
| `not-found`: Lexware has no file in the requested format for this voucher, or does not show it to this API key | The voucher has no document in that format, for example a draft, or `xml` for a voucher that is no XRechnung | Finalize the voucher, or ask for the format `default` or `pdf`. |
| `provider-error`: Lexware did not find the requested endpoint or a referenced object (HTTP 404) | A referenced object, such as a contact or predecessor voucher, does not exist | Check the identifiers the call refers to. |
| `provider-error`: Lexware rejected the data as invalid; check the field values against the tool description | Lexware answered HTTP 406 for the data or file | Correct the values or the file type as the tool description says. |
| `provider-error`: Lexware reports a conflict: the object was changed in the meantime; read it again before changing it | Someone changed the object after Qatlas read it | Read the object again and repeat the change deliberately. |
| `provider-error`: Lexware rejected the request as invalid | HTTP 400: a malformed request or a filter out of range | Check the filter and paging arguments. |
| `rate-limited`: Lexware rate-limited the operation | Too many requests for the key in a short time | Wait and retry. A creation or issue is never repeated by Qatlas; check the voucher list first. |
| `timeout`: Lexware did not answer in time | Lexware did not answer within its own limit | Check <https://status.lexware.de> and retry a read. Check Lexware before you repeat a change. |
| `provider-error`: Lexware rejected the operation (HTTP ...) | Any other status, such as a server error | Check <https://status.lexware.de>. Check Lexware before you repeat a change. |
