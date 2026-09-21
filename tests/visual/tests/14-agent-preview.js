const { test } = require('../fixtures');
const { login } = require('../helpers');

// Agent context preview: what an agent sees of the corpus (the MCP-facing view).
test('14-agent-preview', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/agent`, { waitUntil: 'networkidle' });
  await page.locator('.agent-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(800);
  await snap('01-agent-preview');
  log('Agent preview rendered');
});
