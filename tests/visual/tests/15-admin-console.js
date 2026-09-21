const { test } = require('../fixtures');
const { login } = require('../helpers');

// Admin console: the seeded account is an admin, so /admin renders the console.
// The user roster should include the admin plus the seeded second user.
test('15-admin-console', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/admin`, { waitUntil: 'networkidle' });
  await page.locator('.admin').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(900);
  await snap('01-admin');

  // Rows in the users table; be lenient about the exact class.
  const rowCandidates = ['.user-row', '.admin tbody tr', '.admin .row'];
  let count = 0;
  for (const sel of rowCandidates) {
    const c = await page.locator(sel).count();
    if (c > count) count = c;
  }
  log(`Admin user rows (best-effort): ${count}`);
  await snap('02-admin-users');
});
