const { test } = require('../fixtures');
const { login } = require('../helpers');

// Revision history: 'Rekam roadmap' was edited twice during seeding, so its
// History drawer should list multiple versions, each expandable to its content.
test('04-revision-history', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const row = page.locator('.record').filter({ hasText: 'Rekam roadmap' }).first();
  await row.click();
  await page.locator('.editor-title').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(400);

  // Open the History drawer (header action).
  await page.locator('.mindmap-btn', { hasText: 'History' }).click();
  const drawer = page.locator('.drawer');
  await drawer.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(600);
  await snap('01-history-drawer');

  // Each version says who wrote it by name. A bare uuid answers "what changed"
  // but not "who changed it", which is the question history exists for.
  const authorLine = (await page.locator('.rev__sub').first().innerText()).trim();
  if (/\b[0-9a-f]{8}…/.test(authorLine)) {
    throw new Error(`revision shows a truncated identity id instead of a name: ${JSON.stringify(authorLine)}`);
  }
  log(`Revision byline: ${authorLine.replace(/\s+/g, ' ')}`);

  const revs = page.locator('.rev');
  const n = await revs.count();
  log(`Revisions listed: ${n}`);
  if (n < 2) throw new Error(`expected multiple revisions, got ${n}`);

  // Expand an older revision to preview its content.
  await revs.nth(1).locator('.rev__row').click();
  await page.waitForTimeout(500);
  await snap('02-revision-expanded');
  const preview = await page.locator('.rev.open .rev__content').count();
  log(`Expanded revision shows content: ${preview > 0}`);
  if (preview === 0) throw new Error('expanded revision has no content preview');
});
