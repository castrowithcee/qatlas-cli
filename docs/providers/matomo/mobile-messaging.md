---
description: >
  Describes the Matomo SMS settings tools: reading the MobileMessaging settings and removing existing access, the instance binding they need, and the operations that are deliberately not offered.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo SMS settings

The SMS tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope).

## Binding

The SMS settings belong to the account of the token, or to the instance while management is not delegated, not to a
website. All four tools therefore need a connection whose targets name `instance` and are refused before any secret is
read otherwise. None is in a setup profile: the phone numbers are personal data, and the changes remove access or
change who may manage credentials.

## Reading

`matomo.sms-settings.get` reads one section per call with one request. Credentials are never returned; only whether they
exist and which SMS provider is set. `credit-left` makes the Matomo server ask the SMS provider, so it depends on that
provider and can fail or be slow. `provider` and `credit-left` need the right to manage credentials, which is a Super
User right unless management is delegated.

## Removing access

`matomo.sms-phone-numbers.remove` removes a number of the token's user and, through Matomo, from scheduled reports that
name it. It accepts only the form `+` followed by 5 to 30 digits, which is narrower than Matomo's own cleaning of
spaces and brackets. `matomo.sms-credentials.delete` deletes the stored provider credentials and is offered only where a
connection's `tools` list names it. `matomo.sms-delegation.set` needs Super User rights. All three send one request
that is never repeated and need confirmation.

## Not offered

Storing provider credentials, adding a number, resending a verification code, and verifying a number create new
outbound paths (SMS to third parties or secrets in Matomo) and have no tool. Matomo needs no password confirmation for
the offered methods.
