const { expect } = require('@playwright/test');
const { test } = require('../fixtures');

// /terms and /privacy are the two pages most likely to be linked from outside
// rekam — a signup flow, an email, an app store listing — and they must work
// for someone with no account, which is the whole point of them.
//
// They used to be hash routes inside the app bundle, matched before the branch
// that chose gate-vs-landing, so the risk was someone reordering that branch
// and burying them. They are static pages on the marketing site now, so the
// risks changed: a clean URL that only resolves with a .html suffix, a title
// that never made it out of the SPA's runtime, and the footer links that
// briefly shipped pointing nowhere.
test('21-legal-pages', async ({ page, marketingUrl, snap, log }) => {
  // Clean URLs, no extension and no hash. This is what the footer links to and
  // what anyone else would link to.
  await page.goto(`${marketingUrl}/terms`, { waitUntil: 'networkidle' });
  await page.locator('.legal').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('.legal__doc h1').first()).toHaveText('Terms of Service');
  await expect(page.locator('.legal__doc')).toContainText('1. What this covers');
  // The title is a real <title> in the served HTML now, not something the app
  // sets after boot — so it is right in a link preview and in search results,
  // before any script runs.
  await expect(page).toHaveTitle('Terms of Service — rekam');
  log('Terms renders at a clean URL with a server-side title');
  await page.waitForTimeout(300);
  await snap('21-terms');

  // The back link returns to the landing page.
  await page.click('.legal__back');
  await expect(page).toHaveURL(`${marketingUrl}/`);
  await page.locator('.lp-hero').waitFor({ state: 'visible', timeout: 10000 });
  log('Back link from a legal page returns to the landing page');

  await page.goto(`${marketingUrl}/privacy`, { waitUntil: 'networkidle' });
  await page.locator('.legal').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('.legal__doc h1').first()).toHaveText('Privacy Policy');
  await expect(page.locator('.legal__doc')).toContainText('1. Who this is about');
  await expect(page).toHaveTitle('Privacy Policy — rekam');
  log('Privacy renders at a clean URL with a server-side title');
  await page.waitForTimeout(300);
  await snap('21-privacy');

  // The footer links resolve. This shipped broken once, and it is the only
  // route most readers will ever take to these pages.
  await page.goto(`${marketingUrl}/`, { waitUntil: 'networkidle' });
  for (const [label, path, heading] of [
    ['Terms', '/terms', 'Terms of Service'],
    ['Privacy', '/privacy', 'Privacy Policy'],
  ]) {
    const link = page.locator('.lp-footer__link', { hasText: new RegExp(`^${label}$`) });
    await expect(link).toHaveAttribute('href', path);
    await link.click();
    await expect(page.locator('.legal__doc h1').first()).toHaveText(heading);
    await page.goBack({ waitUntil: 'networkidle' });
  }
  log('Both footer links resolve to their documents');

  // Prose renders as prose: the markdown is converted at build time, so a
  // broken pipeline shows up as visible markdown rather than headings.
  await page.goto(`${marketingUrl}/terms`, { waitUntil: 'networkidle' });
  const headings = await page.locator('.legal__doc h2').count();
  if (headings < 3) {
    throw new Error(`expected the terms to render as headed sections, found ${headings} h2 elements`);
  }
  await expect(page.locator('.legal__doc')).not.toContainText('## 1.');
  log(`Markdown rendered to HTML at build time (${headings} sections)`);
});

// The update manifest is published by the marketing build, because it is the
// only channel that reaches a self-hosted instance about a new release. Every
// server polls it once a day; a 404 here is silent on both ends, which is the
// state issue #35 was filed about.
test('21b-version-manifest', async ({ request, marketingUrl, log }) => {
  const res = await request.get(`${marketingUrl}/version.json`);
  expect(res.ok()).toBeTruthy();
  const manifest = await res.json();
  const editions = Object.keys(manifest);
  expect(editions.length).toBeGreaterThan(0);
  for (const [edition, rel] of Object.entries(manifest)) {
    expect(['solo', 'team', 'managed', 'desktop']).toContain(edition);
    expect(rel.latest).toMatch(/^v\d+\.\d+\.\d+$/);
  }
  log(`version.json served with ${editions.length} edition(s): ${editions.join(', ')}`);
});
