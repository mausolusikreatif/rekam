const { test } = require('../fixtures');
const { login } = require('../helpers');

// Graph views: the link mindmap (how records connect) and the classification map
// (the taxonomy tree). Both render as an overlay canvas.
test('12-mindmap-graph', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  // Link mindmap over the whole corpus.
  await page.goto(`${baseUrl}/#/mindmap`, { waitUntil: 'networkidle' });
  await page.locator('.overlay').waitFor({ state: 'visible', timeout: 12000 });
  await page.waitForTimeout(1800); // let the graph lay out
  await snap('01-mindmap');
  log('Mindmap rendered');

  // Classification map (taxonomy tree) via the sidebar toggle.
  await page.goto(`${baseUrl}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 10000 });
  const mapToggle = page.locator('.graph-toggle[title="Classification map (taxonomy tree)"]');
  if (await mapToggle.count() > 0) {
    await mapToggle.click();
    await page.locator('.overlay').waitFor({ state: 'visible', timeout: 12000 });
    await page.waitForTimeout(1600);
    await snap('02-classification-map');
    log('Classification map rendered');
  } else {
    log('classification map toggle not found');
  }
});
