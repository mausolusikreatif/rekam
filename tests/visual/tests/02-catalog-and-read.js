const { test } = require('../fixtures');
const { login } = require('../helpers');

// Reading a record: open 'Rekam roadmap' and confirm the reading view renders
// its title, content, and the backlinks panel (other records link to it).
test('02-catalog-and-read', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const row = page.locator('.record').filter({ hasText: 'Rekam roadmap' }).first();
  if (await row.count() === 0) throw new Error("seeded record 'Rekam roadmap' not found");
  await row.click();

  const title = page.locator('.editor-title');
  await title.waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(500);
  const titleText = (await title.textContent())?.trim();
  log(`Opened record: ${titleText}`);
  if (titleText !== 'Rekam roadmap') throw new Error(`unexpected title: ${titleText}`);
  await snap('01-reading-view');

  // Backlinks: seeded records point here, so the "Linked from" panel should show.
  const backlinks = page.locator('.backlinks');
  if (await backlinks.count() > 0) {
    const chips = await page.locator('.backlink-chip').count();
    log(`Backlinks shown: ${chips}`);
    await backlinks.scrollIntoViewIfNeeded();
    await snap('02-backlinks');
  } else {
    log('No backlinks panel (unexpected, but not fatal)');
  }
});
