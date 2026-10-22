---
description: >
  Describes the Telegram tools that send photos, documents, videos, audio, voice messages, albums, rich messages, and live photos.
type: knowledge
edit: shared
created: 2026-10-22
updated: 2026-10-22
---

# Media

`telegram.photos.send`, `telegram.documents.send`, `telegram.videos.send`, `telegram.animations.send`,
`telegram.video-notes.send`, `telegram.audio.send`, `telegram.voice.send`, and `telegram.media-groups.send` (`create`)
send one file of that kind or an album. Photos and videos may be mixed in an album, with an optional `type` per
item; documents and audio files form albums of their own, and a voice message is never part of one. Each file
comes from exactly one of `local_path` or `file_ref`, an album takes the choice per item, and a URL is never a
source. The options of [`telegram.messages.send`](messages.md) that apply carry over; an album shows its caption on
its first item, and a video note takes neither caption nor `parse_mode`. A `file_ref` must come from the selected
chat itself; any other one, as under [Files](files.md), is refused before the credential is read, even from another
bound chat. Its size is not known locally; Telegram enforces the limits.

## Rich messages

`telegram.rich-messages.send` (`create`) sends one rich message of the modeled block types, which its schema lists.
Files come only from `local_path` or a `file_ref` of the selected chat, never a URL; buttons follow the
[inline keyboard rules](messages.md#inline-keyboard), with no web-app or login targets, and drafts are not offered.
A repeated send can post twice.

## Live photos

`telegram.live-photos.send` (`create`) sends a live photo: a video of at most 10 seconds and 10 MB from
`local_path` or `file_ref`, and its still image of up to 10 MB from `photo_local_path` or `photo_file_ref`. Both
are required and follow the rules above; the options are those of the other media tools, and a live photo is
never part of an album. Its result `file_ref` is that of the video.
