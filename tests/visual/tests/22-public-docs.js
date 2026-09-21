const { expect } = require('@playwright/test');
const { test } = require('../fixtures');

// The public docs corpus at /docs: rekam's own documentation, seeded from
// internal/docs/corpus/*.md at boot and served read-only to anyone.
//
// It renders in the real application shell rather than a lookalike — same
// sidebar, toolbar, catalog table, command palette, and record view — because
// /docs is the documentation and the product demo at once. So most of what
// this test asserts is that the app's own components are what's on screen, and
// that the pieces which cannot exist without an account are absent rather than
// present-but-broken.
test('22-public-docs', async ({ page, baseUrl, snap, log }) => {
  await page.goto(`${baseUrl}/docs`, { waitUntil: 'networkidle' });

  // The real AppShell: sidebar, toolbar, catalog table.
  await page.locator('.app').waitFor({ state: 'visible', timeout: 10000 });
  await expect(page.locator('.sidebar')).toBeVisible();
  await expect(page.locator('.toolbar')).toBeVisible();
  const rows = page.locator('.record');
  await expect(rows.first()).toBeVisible({ timeout: 10000 });
  const count = await rows.count();
  expect(count).toBeGreaterThan(0);
  log(`Docs catalog lists ${count} records in the app shell`);

  if ((await page.title()) !== 'Docs — rekam') {
    throw new Error(`docs index tab title is ${JSON.stringify(await page.title())}`);
  }

  // The taxonomy tree is built from the seeded corpus.
  await expect(page.locator('.tree-node').first()).toBeVisible();
  await snap('22-docs-catalog');

  // Nothing that writes, and nothing that needs an account, may be on screen.
  // The server refuses these regardless; a public page offering a control that
  // then fails is its own kind of broken.
  // .switcher is the identity and team pickers; .review-nav is the deck.
  for (const gone of ['.sidebar .switcher', '.review-nav', '.lh']) {
    if (await page.locator(gone).count()) {
      throw new Error(`/docs shows ${gone}, which needs an account`);
    }
  }
  const bodyText = await page.locator('body').innerText();
  for (const control of ['New record', 'Delete', 'Export']) {
    if (bodyText.includes(control)) {
      throw new Error(`/docs shows a write control (${control}); it must be read-only`);
    }
  }
  log('No write controls and no account widgets on the docs catalog');

  // Search is the app's command palette, not a bespoke input — same ⌘K modal,
  // backed by the server's FTS.
  await page.locator('.search-trigger').click();
  await expect(page.locator('.cmdk')).toBeVisible({ timeout: 5000 });
  await page.locator('.cmdk__input').fill('taxonomy');
  await page.waitForTimeout(600);
  const hits = page.locator('.cmdk__item');
  await expect(hits.first()).toBeVisible({ timeout: 5000 });
  await snap('22-docs-palette');

  // The palette's Commands group is all writes and authenticated-only views, so
  // it is absent here; Classes and Records carry the feature.
  const paletteText = await page.locator('.cmdk').innerText();
  for (const cmd of ['New record', 'Export catalog', 'Preview agent context']) {
    if (paletteText.includes(cmd)) {
      throw new Error(`command palette offers "${cmd}" on the public docs corpus`);
    }
  }
  log('Palette search works; no commands offered');

  await hits.first().click();

  // Reading a record: the real MemoryEditor in its read-only mode, at a slug
  // URL rather than the app's hash route, so the link is worth sharing.
  await expect(page.locator('.editor-page')).toBeVisible({ timeout: 10000 });
  await expect(page.locator('.readonly-tag')).toBeVisible();
  const url = page.url();
  if (!/\/docs\/[a-z0-9-]+$/.test(url)) {
    throw new Error(`want a slug URL like /docs/the-taxonomy, got ${url}`);
  }
  log(`Record URL: ${url}`);

  // The tab title names the record, not the brand. A /docs link is meant to be
  // pasted into a chat and indexed, and both read the title.
  const recordTitle = (await page.locator('.editor-title').innerText()).trim();
  const tabTitle = await page.title();
  if (!tabTitle.startsWith(recordTitle)) {
    throw new Error(`tab title ${JSON.stringify(tabTitle)} does not name the record ${JSON.stringify(recordTitle)}`);
  }
  log(`Tab title: ${tabTitle}`);
  await snap('22-docs-record');

  // The link graph is the thing being demonstrated, so both directions of it
  // have to work: inbound backlinks, and an outbound [[wiki-link]] in the body.
  await expect(page.locator('.backlinks')).toBeVisible();
  const chip = page.locator('.backlink-chip').first();
  const chipTitle = (await chip.locator('.backlink-chip__title').innerText()).trim();
  await chip.click();
  await expect(page.locator('.editor-title')).toContainText(chipTitle, { timeout: 10000 });
  log(`Followed a backlink to "${chipTitle}"`);

  // Version history is readable here, because the docs describe it and
  // documentation that cannot show its own subject is half a demo. Restoring a
  // version is a write, so that control must not be present.
  await page.locator('.mindmap-btn', { hasText: 'History' }).click();
  const drawer = page.locator('.drawer[aria-label="Revision history"]');
  await expect(drawer).toBeVisible({ timeout: 10000 });
  await page.waitForTimeout(500);
  const drawerText = await drawer.innerText();
  if (!/v\d/.test(drawerText)) {
    throw new Error(`revision drawer listed no versions: ${JSON.stringify(drawerText.slice(0, 200))}`);
  }
  if (/Restore/i.test(drawerText)) {
    throw new Error('revision drawer offers Restore on the public docs corpus');
  }
  if (/\b[0-9a-f]{8}…/.test(drawerText)) {
    throw new Error(`revision byline shows a truncated identity id instead of a name: ${JSON.stringify(drawerText.slice(0, 200))}`);
  }
  await snap('22-docs-history');
  log('Revision history readable, no restore offered');
  await drawer.locator('.close').click();
  await expect(drawer).toBeHidden({ timeout: 5000 });

  const wikilink = page.locator('.markdown-body .wikilink').first();
  if (await wikilink.count()) {
    const target = ((await wikilink.getAttribute('data-wikilink')) || '').trim();
    await wikilink.click();
    await page.waitForTimeout(800);
    expect(page.url()).toMatch(/\/docs\/[a-z0-9-]+$/);
    log(`Followed a wiki-link to "${target}"`);
    await snap('22-docs-wikilink');
  }

  // A deep link has to work cold, with no corpus index in memory yet.
  const deep = page.url();
  await page.goto(deep, { waitUntil: 'networkidle' });
  await expect(page.locator('.editor-page')).toBeVisible({ timeout: 10000 });
  await expect(page.locator('.readonly-tag')).toBeVisible();
  log(`Cold deep link resolved: ${deep}`);

  // The public mirror answers without credentials and refuses writes.
  const anon = await page.request.get(`${baseUrl}/public/memories`);
  expect(anon.status()).toBe(200);
  const wrote = await page.request.post(`${baseUrl}/public/memory`, { data: { title: 'nope' } });
  if (wrote.status() === 200 || wrote.status() === 201) {
    throw new Error(`POST /public/memory succeeded (${wrote.status()}); the mirror must be read-only`);
  }
  log(`Anonymous read 200, anonymous write ${wrote.status()}`);
});
