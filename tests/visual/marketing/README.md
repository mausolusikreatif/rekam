# Landing-page media

Everything under `frontend/public/media/` is generated from a real rekam
instance by `run.sh` in this directory. Nothing on the landing page is a
mockup, a composite, or a drawing of the UI — if a shot looks wrong, the app
looks wrong, and the fix belongs in the app.

## Regenerating

```bash
tests/visual/marketing/run.sh
```

Needs `docker`, `ffmpeg` and `jq`. It builds the frontend and the binary, runs
the server against a throwaway data dir on port 3011, seeds the demo corpus,
drives the UI with Playwright inside the same Docker image the visual suite
uses, then compresses everything with ffmpeg and writes it into
`frontend/public/media/`. Takes about four minutes.

Re-run it whenever the UI changes enough that the shots stop being true. The
output is committed, because a landing page cannot depend on whoever is
building it having Docker.

## What comes out

| File | Used by |
| ---- | ------- |
| `hero.webm` / `hero.mp4` | the hero clip — search, open a record, follow a typed link |
| `hero-poster.webp` | poster for the hero, and for the walkthrough |
| `record.webp` | "A record, and everything it points at" |
| `search.webp` | "Ask for it in the words you'd use" |
| `mindmap.webp` | "The links make a map you can walk" |
| `walkthrough.webm` / `walkthrough.mp4` | the click-to-play longer clip |

Every file above also has one twin per non-light theme in `THEMES`
(`frontend/src/lib/store.js`) — currently `-dark` and `-malleable`
(`hero-dark.webm`, `hero-malleable.webm`, and so on for every file). The app
has no theme-agnostic screen, so `run.sh` shoots the whole set once per theme:
`light`/`dark` drive Playwright's own `colorScheme`; any explicit-only theme
(malleable is never chosen by the OS) is set the same way a real visitor's
saved choice would be — written to `localStorage` before the page's own
pre-paint script reads it (see capture.js). `LandingPage.svelte` picks between
them at runtime off the same signal (an explicit choice, or the OS) the app
itself uses to theme. Regenerating always produces the full set; there is no
per-theme mode.

Adding a theme: give it a row in `THEMES` (both `store.js`, for the picker,
and the `THEMES=(...)` array in `run.sh`, for the capture), a seed block in
`global.css`, and re-run this script. Nothing else names a theme by hand.

Two encodings of each clip because Safari's VP9 support is not something to
bet a hero on; the browser picks one, so only one is ever downloaded. No audio
track at all (`ffmpeg -an`) — the clips have nothing to say, and a silent
track is bytes for nothing.

## Choices worth knowing about

**The corpus is its own.** `seed.sh` here is not `tests/visual/seed.sh`. That
one exists to exercise edge cases — a tombstone, a dangling edge, a second
team — and its record bodies are one-liners, which look thin in a screenshot.
This one files fewer records with real bodies: a shell block, a table, a
mermaid diagram, and typed `[[depends-on::…]]` links, because the point of the
media is to show what a record actually looks like.

**It signs up rather than adopting the bootstrap admin.** The admin account is
created with the hardcoded name `admin`, and the sidebar footer renders that
name. A visitor should not be looking at "admin", so the seed goes through the
real `/ui/signup` flow and the corpus belongs to a person with a name.

**Stills have no cursor; clips do.** `tests/visual/fixtures.js` injects a
visible cursor dot so the suite's recordings show where each interaction
happens. That is right for video and wrong for a still, where it is just a red
dot nobody can explain — so the overlay is injected per-context here rather
than globally.

**Stills are 2×, clips are 1×.** The images are `<img>` on a retina screen and
want the pixels; VP9 at 2× is enormous and buys nothing at the size the clips
are displayed.

**The clips start signed in.** The recording context is handed a `storageState`
captured in a separate, unrecorded context. Otherwise every clip opens on the
login form, which is the one screen a visitor watching a demo does not need.

## Formats and the service worker

`frontend/vite.config.js` precaches `**/*.{js,css,html,svg,png,woff2}`. WebP
and video are deliberately outside that list: precaching a megabyte of
marketing media into every installed app would be a poor trade for a file the
app itself never shows. Keep the stills `.webp` and the clips `.webm`/`.mp4`
and they stay out of the service worker on their own.
