const { test } = require('../fixtures');
const { login } = require('../helpers');

// Agent skills view: the catalog of skills an agent can use against the corpus.
test('13-skills', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/skills`, { waitUntil: 'networkidle' });
  await page.locator('.skills-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(700);
  await snap('01-skills');
  log('Skills view rendered');
});
