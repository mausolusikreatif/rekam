const { expect } = require('@playwright/test');
const { test } = require('../fixtures');

// The landing page's screenshots and clips are generated from a real rekam by
// tests/visual/marketing/run.sh and committed under frontend/public/media/.
// That split — assets built by one process, referenced by another — is exactly
// where things rot silently, so this file guards the seam rather than the
// looks. Every check here is a bug that actually happened while building it:
//
//   * a /ui/media/ path that 404s renders as nothing at all, and scrolling
//     past a blank rectangle does not feel like an error
//   * re-cropping a shot without updating the markup's width/height leaves the
//     browser reserving the wrong box, which shifts the page as it loads
//   * shipping an asset with fewer pixels than it is displayed at is how the
//     first cut of the hero clip ended up soft
//   * adding a format to the service worker's globPatterns would precache
//     megabytes of marketing media into every installed copy of the app
//
// It deliberately does NOT assert on appearance. Whether the shot is well
// framed is a judgement call; whether it is the right size, present, and not
// being upscaled is not.
test('23-landing-media', async ({ page, baseUrl, marketingUrl, snap, log }) => {
  await page.goto(`${marketingUrl}/`, { waitUntil: 'networkidle' });
  await page.locator('.lp-hero').waitFor({ state: 'visible', timeout: 10000 });

  // Scroll the page the way a person would so the lazy plates commit; a
  // fullPage screenshot alone does not reliably trigger them.
  const pageHeight = await page.evaluate(() => document.documentElement.scrollHeight);
  for (let y = 0; y < pageHeight; y += 700) {
    await page.evaluate((to) => window.scrollTo(0, to), y);
    await page.waitForTimeout(150);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.waitForTimeout(800);

  // ── Every referenced file exists, and is served as what it claims to be ──
  const refs = await page.evaluate(() =>
    [...document.querySelectorAll('.lp-plate img, .lp-plate source, .lp-plate video')]
      .flatMap((el) => [el.getAttribute('src'), el.getAttribute('poster')])
      .filter((u) => u && u.startsWith('/media/'))
  );
  const urls = [...new Set(refs)];
  if (urls.length === 0) throw new Error('landing page references no /media/ assets at all');

  const EXPECTED_TYPE = { webp: 'image/webp', webm: 'video/webm', mp4: 'video/mp4' };
  const bytes = {};
  for (const url of urls) {
    const res = await page.request.get(new URL(url, marketingUrl).toString());
    if (!res.ok()) throw new Error(`${url} returned ${res.status()}`);
    const ext = url.split('.').pop();
    const got = (res.headers()['content-type'] || '').split(';')[0];
    if (EXPECTED_TYPE[ext] && got !== EXPECTED_TYPE[ext]) {
      throw new Error(`${url} served as ${got}, expected ${EXPECTED_TYPE[ext]}`);
    }
    bytes[url] = Number(res.headers()['content-length'] || (await res.body()).length);
  }
  log(`${urls.length} media files resolve with the right content-type`);

  // ── Declared dimensions match the actual file ───────────────────────────
  // Re-cropping a shot and forgetting the markup is silent: the image still
  // renders, it just reserves a box of the wrong shape and the page jumps.
  const imgs = await page.evaluate(() =>
    [...document.querySelectorAll('.lp-plate img')].map((i) => ({
      src: i.getAttribute('src'),
      declaredW: Number(i.getAttribute('width')),
      declaredH: Number(i.getAttribute('height')),
      naturalW: i.naturalWidth,
      naturalH: i.naturalHeight,
      renderedW: Math.round(i.getBoundingClientRect().width),
      alt: (i.getAttribute('alt') || '').trim(),
      loading: i.getAttribute('loading'),
    })));
  if (imgs.length === 0) throw new Error('landing page has no plate images');

  for (const img of imgs) {
    if (!img.naturalW) throw new Error(`${img.src} never decoded (naturalWidth 0)`);
    if (!img.declaredW || !img.declaredH) {
      throw new Error(`${img.src} is missing width/height attributes — the page will shift as it loads`);
    }
    if (img.declaredW !== img.naturalW || img.declaredH !== img.naturalH) {
      throw new Error(
        `${img.src} declares ${img.declaredW}x${img.declaredH} but the file is ` +
        `${img.naturalW}x${img.naturalH} — re-crop the shot or update the markup`
      );
    }
    // Alt text on a product screenshot has to say what is in it. A short
    // string here means someone wrote "screenshot" and moved on.
    if (img.alt.length < 40) {
      throw new Error(`${img.src} has thin alt text: ${JSON.stringify(img.alt)}`);
    }
    if (img.loading !== 'lazy') {
      throw new Error(`${img.src} should be loading="lazy" — every plate is below the fold`);
    }
  }
  log(`${imgs.length} plate images: declared size matches the file, all captioned for screen readers`);

  // ── Nothing is being upscaled ───────────────────────────────────────────
  // The asset needs enough pixels for a 2x display at the width the layout
  // actually gives it. This is the check that would have caught the hero clip
  // shipping at 1280 wide for a ~2160px slot.
  const RETINA = 2;
  for (const img of imgs) {
    const needed = img.renderedW * RETINA;
    if (img.naturalW < needed * 0.9) {
      throw new Error(
        `${img.src} is ${img.naturalW}px wide but is displayed at ${img.renderedW}px ` +
        `(${needed}px on a 2x screen) — it will be upscaled and look soft`
      );
    }
    log(`  ${img.src.split('/').pop()}: ${img.naturalW}px for a ${img.renderedW}px slot`);
  }

  const heroVideo = page.locator('.lp-plate--bleed video').first();
  await expect(heroVideo).toBeVisible();
  const vid = await heroVideo.evaluate((v) => ({
    videoW: v.videoWidth,
    renderedW: Math.round(v.getBoundingClientRect().width),
    poster: v.getAttribute('poster'),
    types: [...v.querySelectorAll('source')].map((s) => s.type),
  }));
  for (const type of ['video/webm', 'video/mp4']) {
    if (!vid.types.includes(type)) {
      throw new Error(`hero clip has no ${type} source, got ${JSON.stringify(vid.types)}`);
    }
  }
  if (!vid.poster) throw new Error('hero clip has no poster — it renders as a black box until it buffers');
  // Clips are 1x on purpose: Chrome will give 2x screenshots or a smooth
  // real-time screencast, but not both (2x screenshots cap out near 7fps), and
  // smooth motion won that trade. So the bar for video is 1:1 in CSS pixels,
  // not 2x — but it must never be stretched past its own width, which is what
  // the max-width cap on .lp-plate--bleed exists to prevent.
  if (vid.videoW && vid.videoW < vid.renderedW) {
    throw new Error(
      `hero clip is ${vid.videoW}px wide but displayed at ${vid.renderedW} CSS px — ` +
      `lower the max-width on .lp-plate--bleed or raise CLIP_VIEW in capture.js`
    );
  }
  log(`hero clip: ${vid.videoW}px for a ${vid.renderedW}px slot, webm + mp4, poster set`);

  // ── An autoplaying loop must be stoppable (WCAG 2.2.2) ──────────────────
  const control = page.locator('.lp-plate__control');
  await expect(control).toBeVisible();
  const before = await control.getAttribute('aria-label');
  await control.click();
  await page.waitForTimeout(400);
  const after = await control.getAttribute('aria-label');
  if (before === after) throw new Error(`hero play/pause did not change state (stayed "${before}")`);
  log(`hero clip can be stopped: "${before}" → "${after}"`);

  // ── First-load weight ───────────────────────────────────────────────────
  // Only one of webm/mp4 is ever fetched, and the walkthrough is preload="none",
  // so the real cost is the hero (larger of the two encodings), its poster, and
  // the stills. The ceiling exists to make a regression visible, not to be hit.
  const size = (name) => bytes[`/ui/media/${name}`] || 0;
  const firstLoad =
    Math.max(size('hero.webm'), size('hero.mp4')) +
    size('hero-poster.webp') +
    imgs.reduce((sum, i) => sum + (bytes[i.src] || 0), 0);
  const BUDGET = 4 * 1024 * 1024;
  log(`first-load media: ${(firstLoad / 1024 / 1024).toFixed(2)} MB (budget ${BUDGET / 1024 / 1024} MB)`);
  if (firstLoad > BUDGET) {
    throw new Error(`first-load media is ${(firstLoad / 1024 / 1024).toFixed(2)} MB, over the ${BUDGET / 1024 / 1024} MB budget`);
  }

  // The click-to-play clip must stay opt-in, or it stops being free.
  const walk = page.locator('.lp-plate--walkthrough video').first();
  await expect(walk).toHaveCount(1);
  const preload = await walk.getAttribute('preload');
  if (preload !== 'none') {
    throw new Error(`walkthrough clip has preload="${preload}" — it should be "none" so it costs nothing unless played`);
  }
  log('walkthrough clip is preload="none"');

  // ── Marketing media must stay out of the app entirely ───────────────────
  // This used to be a check that the service worker's globPatterns excluded
  // webp/webm/mp4. The media no longer ships inside the binary at all — the
  // landing page is its own build now — so the stronger statement is
  // available: the app serves none of it, and the service worker cannot
  // precache what is not there. Asserted against the running server rather
  // than the config, so it catches the effect rather than the intent.
  const strayMedia = await page.request.get(`${baseUrl}/ui/media/hero.webm`);
  if (strayMedia.ok()) {
    throw new Error('the app still serves /ui/media/ — marketing assets should not be inside the binary');
  }
  const sw = await page.request.get(`${baseUrl}/ui/sw.js`);
  if (sw.ok()) {
    const body = await sw.text();
    if (body.includes('media/')) {
      throw new Error('the service worker precaches media — marketing assets should not ship inside the installed app');
    }
    log('the app serves no marketing media, and the service worker precaches none');
  }

  await snap('23-landing-media');

  // ── The hero clip must stay sharp on a wide monitor ─────────────────────
  // It bleeds to the window edge, so its slot grows with the viewport while
  // the file does not. That is only safe because LandingPage.svelte caps the
  // plate; this checks the cap is still doing its job at the widths people
  // actually have, rather than only at this suite's 1280.
  for (const width of [1512, 1728, 1920, 2560]) {
    await page.setViewportSize({ width, height: 900 });
    await page.waitForTimeout(500);
    const m = await page.evaluate(() => {
      const v = document.querySelector('.lp-plate--bleed video');
      return { vw: v.videoWidth, slot: Math.round(v.getBoundingClientRect().width) };
    });
    if (m.vw && m.vw < m.slot) {
      throw new Error(
        `at a ${width}px window the hero slot is ${m.slot} CSS but the clip is ` +
        `only ${m.vw}px — either cap .lp-plate--bleed lower or raise CLIP_VIEW ` +
        `in tests/visual/marketing/capture.js`
      );
    }
    log(`  ${width}px window: hero slot ${m.slot} CSS, clip ${m.vw}px — ok`);
  }
});
