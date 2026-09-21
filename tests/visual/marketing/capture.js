// Shoot the landing page's media against a real, seeded rekam.
//
// Two kinds of output, and they want opposite things:
//   stills  — 3x device scale, and NO cursor overlay
//   video   — a big viewport scaled down with CSS zoom, cursor overlay ON,
//             because a clip where things react with nothing touching them is
//             confusing
//
// Video needs its own trick, and two obvious ones do not work.
//
// deviceScaleFactor does NOT raise video resolution: Playwright rasterises a
// recording at CSS-pixel size and then pads it out to the requested size.
// Asking for 2x produced a 2560x1600 file with a 1280x800 picture in the
// top-left corner and grey everywhere else.
//
// CSS zoom on the root does raise it, but it breaks the mindmap:
// Mindmap.svelte sizes its canvas backing store from window.innerWidth, which
// reports the UNZOOMED viewport, so the graph gets drawn outside the visible
// area. A capture pipeline must not depend on a hack that breaks one of the
// views it exists to show.
//
// What does work is Chrome's own screencast (CDP Page.startScreencast). It
// captures the compositor surface, which IS at deviceScaleFactor, so a 1280x800
// viewport at 2x streams 2560x1600 frames: text at its natural size and twice
// the pixels, with no zoom anywhere.
//
// It is damage-based — frames arrive when something changes, not on a clock —
// so each one carries a timestamp and run.sh assembles them through ffmpeg's
// concat demuxer with real per-frame durations. Taking screenshots in a loop
// instead was tried and could not keep up: a 2560x1600 PNG costs ~400ms to
// encode, which turned a 12-second flow into 31 frames and played it back at
// four times speed.

const { chromium } = require('/tests/node_modules/playwright');
const fs = require('fs');
const path = require('path');

const BASE = process.env.REKAM_DEMO_URL || 'http://localhost:3011';
const EMAIL = process.env.REKAM_DEMO_EMAIL || 'ada@rekam.local';
const PW = process.env.REKAM_DEMO_PASSWORD || 'catalog-of-records';
const OUT = process.env.OUT_DIR || '/output';
const FRAME_OUT = path.join(OUT, 'frames');

// Every shot is taken once per theme in THEMES (lib/store.js) — the app has
// no theme-agnostic state, so there is no such thing as one recording that
// works for all of them. run.sh invokes this script once per THEME and each
// run lands under its own suffix (see SUFFIX), light being the one with no
// suffix since it's the bare default.
//
// 'light' and 'dark' are driven by colorScheme on the context: the app's CSS
// keys off prefers-color-scheme for those two, the same signal a real
// visitor's browser sends, so no app-level override is needed. Any other
// theme (malleable, or whatever gets added next) is never chosen by the OS —
// it's always an explicit pick — so it's set the same way a real visitor's
// choice would be: written to localStorage before the page's own pre-paint
// script (index.html) reads it, via an init script on every context below.
const THEME = process.env.REKAM_CAPTURE_THEME || 'light';
const SUFFIX = THEME === 'light' ? '' : `-${THEME}`;
const EXPLICIT_THEME = THEME !== 'light' && THEME !== 'dark';
// Playwright's colorScheme only knows light/dark; an explicit theme still
// needs a baseline so native form controls etc. render sanely underneath it,
// and every explicit theme shipped so far is a dark one.
const CTX_COLOR_SCHEME = THEME === 'light' ? 'light' : 'dark';

async function withTheme(ctx) {
  if (EXPLICIT_THEME) {
    await ctx.addInitScript((t) => { try { localStorage.setItem('rekam.theme', t); } catch (_) {} }, THEME);
  }
  return ctx;
}

const VIEW = { width: 1280, height: 800 };

// Clips: rasterise at 2160x1350, lay out as though the window were 960x600.
// 960 frames the app the way the record still is framed. 2160 is what a 1080
// CSS slot needs on a 2x screen, and the hero's slot is capped at exactly that
// in LandingPage.svelte — the two numbers are a pair, so if one moves the
// other has to. An earlier cut rastered at 1728 and was sharp to about a
// 1512px window, then went soft on every larger one.
const CLIP_VIEW = { width: 1280, height: 800 };
const CLIP_SCALE = 2;      // → 2560x1600 frames

// Same overlay the visual suite uses, so the clips look like the recordings the
// team already reads.
function mouseHelper(zoom) {
  const attach = () => {
    if (document.getElementById('pw-cursor')) return;
    const style = document.createElement('style');
    style.textContent = `
      #pw-cursor {
        position: fixed; top: 0; left: 0; width: 20px; height: 20px;
        margin: -10px 0 0 -10px; border-radius: 50%;
        background: rgba(176,80,60,.30); border: 2px solid #b4503c;
        box-shadow: 0 0 8px rgba(176,80,60,.5);
        pointer-events: none; z-index: 2147483647;
        transition: width .1s, height .1s, margin .1s, background .1s;
      }
      #pw-cursor.click { width: 34px; height: 34px; margin: -17px 0 0 -17px; background: rgba(176,80,60,.15); }`;
    const dot = document.createElement('div');
    dot.id = 'pw-cursor';
    document.head.appendChild(style);
    document.body.appendChild(dot);
    // The dot lives inside the zoomed root, so its own left/top are multiplied
    // by the zoom when painted, while clientX/clientY arrive already in visual
    // coordinates. Divide, or the cursor drifts further from the pointer the
    // further right it goes.
    document.addEventListener('mousemove', (e) => {
      dot.style.left = (e.clientX / zoom) + 'px';
      dot.style.top = (e.clientY / zoom) + 'px';
    }, true);
    document.addEventListener('mousedown', () => dot.classList.add('click'), true);
    document.addEventListener('mouseup', () => dot.classList.remove('click'), true);
  };
  if (document.body) attach();
  else window.addEventListener('DOMContentLoaded', attach);
}

// Kill caret blink and any transition mid-flight, so a still is never caught
// half-way through a fade or with a cursor bar burnt into an input.
const STILL_CSS = `
  *, *::before, *::after { transition: none !important; animation: none !important; caret-color: transparent !important; }
`;

// Sign in once, in a context that is not being recorded, and reuse the session
// everywhere else. Otherwise every clip opens on the login form — which is the
// one screen a visitor watching a product demo does not need to see.
async function signIn(browser) {
  const ctx = await withTheme(await browser.newContext({ viewport: VIEW, colorScheme: CTX_COLOR_SCHEME }));
  const page = await ctx.newPage();
  await login(page);
  const state = await ctx.storageState();
  await ctx.close();
  return state;
}

async function login(page) {
  await page.goto(`${BASE}/#/login`, { waitUntil: 'networkidle' });
  await page.locator('#gate-email').waitFor({ state: 'visible', timeout: 15000 });
  await page.fill('#gate-email', EMAIL);
  await page.fill('#gate-pw', PW);
  await page.click('.gate__submit');
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 20000 });
  await page.waitForTimeout(800);
}

async function openRecord(page, title) {
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible' });
  await page.waitForTimeout(400);
  await page.locator('.mem-row, li', { hasText: title }).first().click();
  await page.waitForTimeout(1200);
}

// ── Stills ──────────────────────────────────────────────────────────────────
async function stills(browser, storageState) {
  // 3x, not 2x. Two of these shots are clipped to a region much narrower than
  // the window — the record to a 960px column, search to the palette alone —
  // and at 2x the clipped pixel count lands under the width they are encoded
  // to, which means upscaling a crop. Every still should be downscaled into
  // its target, never stretched up to it.
  const ctx = await withTheme(await browser.newContext({ viewport: VIEW, deviceScaleFactor: 3, storageState, colorScheme: CTX_COLOR_SCHEME }));
  const page = await ctx.newPage();
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 20000 });
  await page.waitForTimeout(600);
  await page.addStyleTag({ content: STILL_CSS });

  const shot = async (name, opts = {}) => {
    await page.addStyleTag({ content: STILL_CSS });
    await page.waitForTimeout(300);
    await page.screenshot({ path: path.join(OUT, `${name}${SUFFIX}.png`), ...opts });
    console.log(`  still: ${name}`);
  };

  // 1. A record, which is the thing the whole product is about. (The catalog
  //    itself is not shot separately: the hero clip opens on it, so the poster
  //    frame already carries that view.)
  //
  //    Shot in a taller window than the rest and clipped to the bottom of the
  //    backlinks block, so the plate ends on real content. At the standard
  //    800px height this record is cut off mid-sentence, which reads as a
  //    rendering fault rather than as a crop — and the backlinks are half the
  //    point of showing a record at all.
  //    The window is also narrower than the rest: the record view centres its
  //    content at about 535px whatever the window, so at 1280 the shot is a
  //    thin column with ~370px of blank paper down each side. At 960 the same
  //    content fills the plate.
  await page.setViewportSize({ width: 960, height: 1400 });
  await openRecord(page, 'Deploy runbook');
  await page.waitForTimeout(600);
  const tail = page.locator('.backlinks').first();
  let recordClip;
  if (await tail.count()) {
    const tb = await tail.boundingBox();
    if (tb) {
      recordClip = {
        x: 0, y: 0,
        width: 960,
        height: Math.min(1400, Math.ceil(tb.y + tb.height + 34)),
      };
    }
  }
  await shot('record', recordClip ? { clip: recordClip } : {});
  await page.setViewportSize(VIEW);

  // 2. The catalog on its own, which is the view the hero clip opens on. It
  //    becomes the clip's poster: what a visitor sees with reduced motion on,
  //    or before the video buffers, so it has to be a real 3x screenshot
  //    rather than an upscaled frame lifted out of a 1x clip.
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible' });
  await page.waitForTimeout(900);
  await shot('hero-poster');

  // 3. Search — the command palette over the whole catalog, not cropped to
  //    the palette. These sections run full width now, so the window fits.
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(500);
  await page.keyboard.press('Control+k');
  await page.waitForTimeout(400);
  await page.keyboard.type('release', { delay: 25 });
  await page.waitForTimeout(900);
  await shot('search');
  await page.keyboard.press('Escape');

  // 4. The mindmap. Zoomed until the labels read, but the whole window
  //    including the legend — the legend is what explains the edge types.
  await page.goto(`${BASE}/#/mindmap`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2500);          // let the force layout settle
  const canvas = await page.locator('canvas').first();
  const box = await canvas.boundingBox();
  if (box) {
    const cx = box.x + box.width / 2;
    const cy = box.y + box.height / 2;
    await page.mouse.move(cx, cy);
    for (let i = 0; i < 5; i++) {
      await page.mouse.wheel(0, -120);
      await page.waitForTimeout(150);
    }
    await page.waitForTimeout(1200);
  }
  await shot('mindmap');

  await ctx.close();
}

// ── Video ───────────────────────────────────────────────────────────────────
async function clip(browser, name, flow, storageState) {
  const dir = path.join(FRAME_OUT, name);
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });

  const ctx = await withTheme(await browser.newContext({
    viewport: CLIP_VIEW,
    deviceScaleFactor: CLIP_SCALE,
    storageState,
    colorScheme: CTX_COLOR_SCHEME,
  }));
  await ctx.addInitScript(mouseHelper, 1);
  const page = await ctx.newPage();
  // Already authenticated, so the first frame is the catalog, not a form.
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 20000 });
  await page.waitForTimeout(700);

  const cdp = await ctx.newCDPSession(page);
  const stamps = [];
  cdp.on('Page.screencastFrame', async (frame) => {
    const i = stamps.length;
    fs.writeFileSync(path.join(dir, `f${String(i).padStart(5, '0')}.jpg`), Buffer.from(frame.data, 'base64'));
    stamps.push(frame.metadata.timestamp);
    try {
      await cdp.send('Page.screencastFrameAck', { sessionId: frame.sessionId });
    } catch (_) {
      // The session is gone because the flow finished; nothing to ack.
    }
  });

  await cdp.send('Page.startScreencast', {
    format: 'jpeg',
    quality: 92,
    maxWidth: CLIP_VIEW.width * CLIP_SCALE,
    maxHeight: CLIP_VIEW.height * CLIP_SCALE,
    everyNthFrame: 1,
  });

  await flow(page);
  await page.waitForTimeout(900);
  await cdp.send('Page.stopScreencast');
  await page.waitForTimeout(250);

  // ffmpeg's concat demuxer holds each frame for its own duration, which is
  // what keeps a damage-based stream in real time: a still second is one frame
  // held for a second, not a second of dropped frames.
  const HOLD_LAST = 0.6;
  const lines = [];
  for (let i = 0; i < stamps.length; i++) {
    const next = i + 1 < stamps.length ? stamps[i + 1] : stamps[i] + HOLD_LAST;
    const d = Math.min(Math.max(next - stamps[i], 0.02), 2.0);
    lines.push(`file 'f${String(i).padStart(5, '0')}.jpg'`);
    lines.push(`duration ${d.toFixed(4)}`);
  }
  if (stamps.length) lines.push(`file 'f${String(stamps.length - 1).padStart(5, '0')}.jpg'`);
  fs.writeFileSync(path.join(dir, 'frames.txt'), lines.join('\n') + '\n');

  const span = stamps.length ? (stamps[stamps.length - 1] - stamps[0] + HOLD_LAST) : 0;
  await page.close();
  await ctx.close();
  console.log(`  clip: ${name} (${stamps.length} frames, ${span.toFixed(1)}s)`);
}

// The hero loop. Short, and it has to read with no sound and no context:
// find a record, open it, follow a typed link. That is the whole product.
async function heroFlow(page) {
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1400);
  await page.mouse.move(640, 400, { steps: 20 });
  await page.keyboard.press('Control+k');
  await page.waitForTimeout(700);
  await page.keyboard.type('deploy', { delay: 130 });
  await page.waitForTimeout(1100);
  await page.keyboard.press('Enter');
  await page.waitForTimeout(1800);
  // Follow a wiki-link chip out of the record into the record it points at.
  const chip = page.locator('a, button', { hasText: 'Release checklist' }).first();
  if (await chip.count()) {
    const b = await chip.boundingBox();
    if (b) {
      await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 24 });
      await page.waitForTimeout(500);
      await page.mouse.click(b.x + b.width / 2, b.y + b.height / 2);
      await page.waitForTimeout(2000);
    }
  }
}

// The longer walkthrough, for the visitor who clicks play.
async function walkthroughFlow(page) {
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1600);

  // Catalog → a record
  const row = page.locator('.mem-row, li', { hasText: 'Deploy runbook' }).first();
  const rb = await row.boundingBox();
  if (rb) {
    await page.mouse.move(rb.x + 200, rb.y + rb.height / 2, { steps: 22 });
    await page.waitForTimeout(500);
    await page.mouse.click(rb.x + 200, rb.y + rb.height / 2);
  }
  await page.waitForTimeout(2400);

  // Scroll the record so the linked-from / related block comes into view.
  await page.mouse.wheel(0, 420);
  await page.waitForTimeout(1800);
  await page.mouse.wheel(0, -420);
  await page.waitForTimeout(900);

  // The graph the links build.
  await page.goto(`${BASE}/#/mindmap`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(3000);
  await page.mouse.move(640, 400);
  for (let i = 0; i < 4; i++) { await page.mouse.wheel(0, -120); await page.waitForTimeout(220); }
  await page.waitForTimeout(1600);

  // Link health.
  await page.goto(`${BASE}/#/links`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(2400);

  // Search.
  await page.goto(`${BASE}/#/`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(900);
  await page.keyboard.press('Control+k');
  await page.waitForTimeout(600);
  await page.keyboard.type('freeze window', { delay: 120 });
  await page.waitForTimeout(1800);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(800);
}

(async () => {
  fs.mkdirSync(OUT, { recursive: true });
  fs.mkdirSync(FRAME_OUT, { recursive: true });
  const browser = await chromium.launch();
  console.log(`→ signing in (${THEME})`);
  const storageState = await signIn(browser);
  console.log('→ stills');
  await stills(browser, storageState);
  console.log('→ clips');
  await clip(browser, `hero${SUFFIX}`, heroFlow, storageState);
  await clip(browser, `walkthrough${SUFFIX}`, walkthroughFlow, storageState);
  await browser.close();
  console.log(`✓ capture complete (${THEME})`);
})();
