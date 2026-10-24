---
description: >
  Describes the Matomo version and website tools: reading the instance version and plugin activation, and listing and reading websites inside the targets.
type: knowledge
edit: shared
created: 2026-10-24
updated: 2026-10-24
---

# Matomo sites and version

The version and website tools of the [Matomo provider](README.md). Version checks and availability are described
under [Version and availability](README.md#version-and-availability); targets under [Scope](README.md#scope).

## Websites

With `site/ID` targets and no `pattern`, `matomo.sites.list` pages the bound IDs in order and reads only the
websites of the batch. With a `pattern`, it reads at most 1000 matching websites and keeps those inside the targets;
when the pattern matches that many, the call is refused as an invalid request asking for a narrower pattern, so a
bound website is never missing silently.