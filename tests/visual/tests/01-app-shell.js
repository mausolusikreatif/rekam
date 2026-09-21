const { test } = require('../fixtures');
const { login } = require('../helpers');

// App shell: after login the catalog lists the seeded records and the sidebar
// shows the classes tree, review nav, and the workspace switcher.
test('01-app-shell', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);
  await snap('01-catalog');

  const records = page.locator('.record');
  const count = await records.count();
  log(`Catalog records: ${count}`);
  if (count === 0) throw new Error('catalog is empty — seed did not run');

  const sidebar = page.locator('.sidebar');
  await sidebar.waitFor({ state: 'visible' });
  const classes = await page.locator('.tree-node').count();
  log(`Taxonomy tree nodes: ${classes}`);
  await snap('02-sidebar');

  // The workspace switcher is present (Personal at minimum).
  const switcher = page.locator('.switcher .current');
  if (await switcher.count() === 0) throw new Error('workspace switcher missing');
  log('Workspace switcher present');
  await snap('03-shell-ready');
});
