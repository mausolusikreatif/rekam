---
title: Images and video
taxonomy: docs.model
---

Paste a picture's URL on its own line and rekam shows the picture. Same for a
clip — an `.mp4` or `.webm` link becomes a player, and a YouTube or Vimeo link
becomes an embedded video. If you prefer to write it out, `![caption](url)`
works too.

A link you gave your own words to stays a link:

```
https://example.com/dashboard.png          →  the picture
![Q3 dashboard](https://example.com/dashboard.png)  →  the picture
[the Q3 dashboard](https://example.com/dashboard.png)  →  a link
```

## rekam does not store the picture

This is the part worth knowing. rekam stores your **record**; the image stays
wherever you put it. Your browser fetches it when you open the record, straight
from that host — rekam never sees it, never copies it, and never has it.

Three things follow, and none of them are bugs:

**A link can die.** If the file moves or the host goes away, the picture goes
with it. The record still says what it said; the image slot becomes a card
telling you where it used to live so you can go and look.

**It may work for you and not for a teammate.** Access is decided by the host
against *the reader's* account, not yours. A Google Drive file shared with only
you renders for you and shows a card for everyone else in the workspace. If a
picture matters to the team, share it so anyone with the link can open it —
before pasting it.

**Some links can never render inline.** A Google Drive or Dropbox *share* link
points at a web page, not at an image file, so there is nothing for the browser
to draw. rekam leaves those as links rather than showing you a broken image
forever. To embed one, you need a direct link to the file itself.

## When you want it to last

Anything you paste as a link is only as durable as the thing hosting it. For a
picture that must still be there in three years — the diagram the decision
rests on, the screenshot of the incident — attach it instead of linking it.
Attachments live in your own store, get backed up with it, and travel with an
export.

Links are for convenience. Attachments are for permanence. Most records want
the first; a few really want the second.
