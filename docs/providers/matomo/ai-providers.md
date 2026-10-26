---
description: >
  Describes the Matomo tools for the AIProviders plugin: reading the AI provider settings without any API key and disconnecting a provider.
type: knowledge
edit: shared
created: 2026-10-26
updated: 2026-10-26
---

# Matomo AI providers

The AI provider tools of the [Matomo provider](README.md). Targets are described under [Scope](README.md#scope).

## Tools

| Tool | Does |
| --- | --- |
| `matomo.ai-providers.get-settings` | reads the providers, the default, and each connection without its API key (Super User, target `instance`) |
| `matomo.ai-providers.disconnect` | removes the stored connection of one provider (Super User, target `instance`; effect `delete`, only via `tools`) |

## Availability

Both tools need the Matomo plugin `AIProviders` to be activated and refuse otherwise as an unsupported capability,
before any plugin method is called. They are in no profile and need a connection whose targets name `instance`.

## Secrets

An API key never appears in a result, not even masked or in part: `matomo.ai-providers.get-settings` reports only
`api_key_set` per provider. Endpoint addresses are shown without credentials, query, or fragment. Texts are
untrusted and cut at a limit.

## Disconnecting

`matomo.ai-providers.disconnect` deletes the key and endpoint of one provider for good, so the key has to be entered
again to reconnect. It sends one request that is never repeated; after an unclear result read the settings before
trying again. An instance that forces its provider from configuration may keep the forced values.
