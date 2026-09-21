const { test } = require('../fixtures');
const { login } = require('../helpers');

// Link health: /links summarizes edges and lists dangling wiki-links. The seed
// includes a record pointing at an unwritten title, so a dangling link shows.
test('11-links-view', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/links`, { waitUntil: 'networkidle' });
  await page.locator('.links-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(800);
  await snap('01-link-health');

  const stats = await page.locator('.stat .stat__num').allTextContents();
  log(`Link stats: ${stats.join(' / ') || '(none)'}`);
  if (stats.length === 0) throw new Error('link health stats did not render');
  await snap('02-links');
});
