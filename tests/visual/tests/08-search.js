const { test } = require('../fixtures');
const { login } = require('../helpers');

// Search: the command palette finds records by title and opens one.
test('08-search', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  // Open the palette from the toolbar trigger.
  await page.locator('.search-trigger').click();
  const input = page.locator('.cmdk__input');
  await input.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(300);
  await snap('01-palette-open');

  await input.fill('roadmap');
  await page.waitForTimeout(900); // debounced search
  const items = page.locator('.cmdk__item');
  const n = await items.count();
  log(`Palette results for "roadmap": ${n}`);
  await snap('02-results');
  if (n === 0) throw new Error('no search results for a seeded title');

  // Open the first result and confirm we land on a reading view.
  await items.first().click();
  await page.locator('.editor-title').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(400);
  const title = (await page.locator('.editor-title').textContent())?.trim();
  log(`Opened from search: ${title}`);
  await snap('03-opened');
});
