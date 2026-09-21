const { expect } = require('@playwright/test');
const { test } = require('../fixtures');

// Public marketing landing page: what an anonymous visitor sees at "/" before
// logging in. It's a pure UI surface (no spec/*.md coverage — the spec is just
// "renders correctly for a logged-out visitor"), but it has a real bug history:
// a "Trusted by teams shipping autonomous agents" bar with fabricated customer
// logos (Helix, Arbor, Lumen, Mesa, Flux) was shipped and later ripped out, and
// separately the footer briefly shipped with no working Terms/Privacy links.
// This test is a regression guard against both classes of mistake recurring,
// plus a basic smoke check that the landing → login handoff still works.
test('20-landing-page', async ({ page, baseUrl, marketingUrl, snap, log }) => {
  await page.goto(`${marketingUrl}/`, { waitUntil: 'networkidle' });
  await page.locator('.lp-hero').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(300);

  // Hero: headline, sub-copy, and the primary "Start free" CTA.
  await expect(page.locator('.lp-hero__title')).toBeVisible();
  await expect(page.locator('.lp-hero__lead')).toBeVisible();
  const heroCta = page.locator('.lp-hero__actions button.btn-primary');
  await expect(heroCta).toBeVisible();
  await expect(heroCta).toContainText('Start free');

  // Nav: the three section anchors, Log in, plus the nav "Start free" CTA.
  const navLinkTexts = (await page.locator('.lp-nav__link').allInnerTexts()).map((t) => t.trim());
  log(`Nav links: ${navLinkTexts.join(' | ')}`);
  for (const label of ['What it stores', 'How it works', 'Pricing', 'Log in']) {
    if (!navLinkTexts.includes(label)) throw new Error(`nav link "${label}" missing, got: ${JSON.stringify(navLinkTexts)}`);
  }
  await expect(page.locator('.lp-nav__cta')).toContainText('Start free');
  await snap('20-hero');

  // The hero used to hold a record drawn by hand in this component's markup.
  // It didn't look like the app — the real record view renders wiki-links as
  // typed chips, not underlined prose — so it was replaced with a clip of the
  // actual UI, shot by tests/visual/marketing/run.sh. This assertion exists so
  // nobody quietly reintroduces a mockup; everything else about the media
  // (sizes, weight, upscaling, the service worker) lives in 23-landing-media.
  const heroClip = page.locator('.lp-plate--bleed video');
  await expect(heroClip).toBeVisible();
  if (await page.locator('.lp-wl, .lp-edge').count()) {
    throw new Error('the hand-drawn hero record is back — the hero should show the real app, not a mockup of it');
  }
  log('Hero shows a clip of the real app, not a hand-drawn record');
  await snap('20-hero-clip');

  // Regression guard: no fabricated social-proof / trust-logo content anywhere
  // on the page. This is the one assertion in this file that matters most.
  const bodyText = await page.locator('body').innerText();
  if (/trusted by/i.test(bodyText)) {
    throw new Error('landing page contains a "Trusted by ..." social-proof claim — this must stay removed');
  }
  for (const fakeLogo of ['Helix', 'Arbor', 'Lumen', 'Mesa', 'Flux']) {
    if (bodyText.includes(fakeLogo)) {
      throw new Error(`landing page contains fabricated trust-logo text "${fakeLogo}" — this must stay removed`);
    }
  }
  log('No fabricated trust-logo / "Trusted by" content found');

  // Regression guard: rekam is not open source. No link may point at a source
  // host, and nothing may offer to show visitors the code or take an issue
  // report there. Docs live at /docs — a public read-only corpus — not in a
  // README.
  const hrefs = await page.locator('a[href]').evaluateAll((as) => as.map((a) => a.getAttribute('href')));
  const codeHosts = /github\.com|gitlab\.com|bitbucket\.org|sourcehut|codeberg/i;
  const offending = hrefs.filter((h) => codeHosts.test(h || ''));
  if (offending.length) {
    throw new Error(`landing page links to a source host, but rekam is not open source: ${JSON.stringify(offending)}`);
  }
  if (codeHosts.test(bodyText) || /\bopen[- ]source\b/i.test(bodyText)) {
    throw new Error('landing page text mentions a source host or claims to be open source');
  }
  log(`No source-host links (checked ${hrefs.length} hrefs)`);

  // Full split (edge/wrangler.toml): docs lives on the app's own origin, not
  // this one — the link has to be an absolute URL ending in /docs, not a
  // path (there is no proxy on this origin for a relative path to resolve
  // against) and not a hash route (that would silently render the landing
  // page again).
  const docsLinks = hrefs.filter((h) => /docs/i.test(h || ''));
  if (docsLinks.length === 0) {
    throw new Error('landing page has no link to the docs');
  }
  for (const href of docsLinks) {
    if (!/^https?:\/\/[^/]+\/docs$/.test(href || '')) {
      throw new Error(`docs link should be an absolute URL ending in "/docs", got ${JSON.stringify(href)}`);
    }
  }
  log(`Docs links point at an absolute .../docs URL (${docsLinks.length})`);

  // The href is only half the story. On the old split-domain release /docs
  // was a real path on the app's origin but every *other* link stayed
  // relative to the marketing site, so clicking it left rekam.net and never
  // came back — and a test that stopped at the string never noticed. Follow
  // the link the way a visitor does; landing on the app's own origin (baseUrl
  // in this harness, REKAM_APP_ORIGIN in the real build) is now correct,
  // not a regression.
  await page.locator('.lp-footer__link', { hasText: 'Docs' }).click();
  await page.waitForURL(new RegExp(`^${baseUrl.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/docs$`), { timeout: 10000 });
  await page.locator('.app').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('.sidebar')).toBeVisible();
  log('Following /docs reaches the docs corpus at the app origin');
  await page.goto(`${marketingUrl}/`, { waitUntil: 'networkidle' });

  // Features section — one entry per row (not a packed grid), each carrying the
  // taxonomy path it lives under. See LandingPage.svelte's .lp-entry.
  await page.locator('#features').scrollIntoViewIfNeeded();
  await page.waitForTimeout(400);
  await expect(page.locator('#features .lp-section__title')).toBeVisible();
  const entries = page.locator('.lp-entry');
  expect(await entries.count()).toBeGreaterThan(0);
  expect(await page.locator('.lp-entry__path').count()).toBe(await entries.count());
  await snap('20-features');

  // Pricing teaser — billing is wired up (Dodo Payments), so this section
  // quotes real numbers and links out to the full /pricing page rather than
  // asking for a beta signup with "price later".
  await page.locator('#pricing').scrollIntoViewIfNeeded();
  await page.waitForTimeout(400);
  await expect(page.locator('#pricing .lp-section__title')).toBeVisible();
  const pricingCta = page.locator('#pricing button.btn-primary');
  await expect(pricingCta).toContainText('Start free');
  const pricingLink = page.locator('#pricing a.lp-textlink', { hasText: 'See full pricing' });
  await expect(pricingLink).toHaveAttribute('href', '/pricing');
  const pricingText = await page.locator('#pricing').innerText();
  if (!/\$\s?\d/.test(pricingText)) {
    throw new Error(`pricing section quotes no price, but billing is wired up: ${JSON.stringify(pricingText)}`);
  }
  log('Pricing section quotes real numbers and links to /pricing');
  await snap('20-pricing');

  // Footer: Terms and Privacy must be real links to the legal routes, not dead
  // or missing — the specific thing that regressed before. They're their own
  // static pages on the marketing site (marketing/terms.html, privacy.html —
  // see vite.marketing.config.js), not app hash routes, so the path is plain.
  const termsLink = page.locator('.lp-footer__link', { hasText: 'Terms' });
  const privacyLink = page.locator('.lp-footer__link', { hasText: 'Privacy' });
  await expect(termsLink).toBeVisible();
  await expect(privacyLink).toBeVisible();
  await expect(termsLink).toHaveAttribute('href', '/terms');
  await expect(privacyLink).toHaveAttribute('href', '/privacy');
  log(`Footer Terms href: ${await termsLink.getAttribute('href')}`);
  log(`Footer Privacy href: ${await privacyLink.getAttribute('href')}`);

  // Landing and the gate are two logically distinct views sharing the same
  // unauthenticated branch — confirm "Start free" actually gets you there and
  // not to a dead end. It must land on *signup*, not on a log-in form: asking
  // someone who clicked "Start free" for a password they don't have yet was a
  // real dead end, and #/signup is what fixed it.
  // Full split: the call to action crosses into the app's own origin now
  // (REKAM_APP_ORIGIN — baseUrl in this harness, app.rekam.net for real),
  // not a relative /ui/ path on this one. Following it for real is the
  // actual assertion here, not a formality — a relative path would silently
  // resolve against this origin instead and 404, which page.url() catches.
  await page.locator('.lp-hero').scrollIntoViewIfNeeded();
  await Promise.all([
    page.waitForURL('**/ui/#/signup', { timeout: 10000 }).catch(() => {}),
    heroCta.click(),
  ]);
  expect(page.url()).toBe(`${baseUrl}/ui/#/signup`);
  log('Start free hands off to the app at its own origin, /ui/#/signup');

  await page.goto(`${marketingUrl}/`, { waitUntil: 'networkidle' });
  const loginHref = await page.locator('.lp-nav__link', { hasText: 'Log in' }).getAttribute('href');
  expect(loginHref).toBe(`${baseUrl}/ui/#/login`);
  log('the nav Log in link points at the app origin');

  // The other half: those two app URLs really do open the two distinct doors.
  await page.goto(`${baseUrl}/ui/#/signup`, { waitUntil: 'networkidle' });
  await page.locator('.gate').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('#su-email')).toBeVisible();
  await expect(page.locator('.gate__title')).toContainText('Start your catalog');
  await page.waitForTimeout(300);
  await snap('20-signup-gate');
  log('/ui/#/signup opens the gate on its signup state');

  await page.goto(`${baseUrl}/ui/#/login`, { waitUntil: 'networkidle' });
  await page.locator('.gate').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('#gate-email')).toBeVisible();
  log('/ui/#/login still opens the log-in state');
});
