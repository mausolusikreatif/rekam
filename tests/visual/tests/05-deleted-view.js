const { test } = require('../fixtures');
const { login } = require('../helpers');

// Deleted records: the seed deleted 'Contacts — design team', so the #/deleted
// audit view lists its tombstone, and opening it shows the tombstone detail.
test('05-deleted-view', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/deleted`, { waitUntil: 'networkidle' });
  await page.locator('.del-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(600);
  await snap('01-deleted-list');

  const tombs = page.locator('.tomb');
  const n = await tombs.count();
  log(`Tombstones listed: ${n}`);
  if (n === 0) throw new Error('deleted view is empty — expected a seeded tombstone');

  // Open the tombstone detail.
  await tombs.first().click();
  const detail = page.locator('.tomb__eyebrow', { hasText: 'Deleted record' });
  await detail.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(500);
  const title = (await page.locator('.tomb__title').textContent())?.trim();
  log(`Tombstone detail: ${title}`);
  await snap('02-tombstone-detail');
});
