---
description: >
  Describes the Telegram bot identity, webhook status, commands, menu button, profile, and default administrator rights tools.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Bot

`telegram.bot.get` (`read`) shows the identity of the bot behind the token. It runs on every connection, since it
concerns only the connection's own token; every other tool of this group needs the `bot` target.
`telegram.webhook.get` (`read`) shows whether a webhook is active, the number of waiting updates, and the time of
the last delivery error, never the webhook URL.

## Commands and menu button

`telegram.commands.get` (`read`), `telegram.commands.set` (`update`), and `telegram.commands.delete` (`delete`,
`tools` list) read, replace, and remove the commands of one `scope` and `language_code`; the scopes `chat`,
`chat_administrators`, and `chat_member` require a named bound `chat`. `telegram.menu-button.get` (`read`) never
shows a web app URL, and `telegram.menu-button.set` (`update`) sets `commands` or `default`, never a web app. Both
address the default button or, with a bound `chat` given by numeric ID, that private chat.

## Profile and default rights

`telegram.profile.get` (`read`) and `telegram.profile.set-name`, `.set-description`, and `.set-short-description`
(`update`) read and set the bot's texts per `language_code`; an empty value removes that language's version.
`telegram.profile.set-photo` (`update`) sets a static JPEG from a `local_path`, and `telegram.profile.remove-photo`
(`delete`, `tools` list) removes it. `telegram.default-admin-rights.get` (`read`) and
`telegram.default-admin-rights.set` (`update`, `tools` list) read and replace the rights the bot requests when
added, every right given explicitly.
