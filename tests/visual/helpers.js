// Shared helpers for the rekam visual suite. These target the running rekam
// SPA (Svelte) served by the Go binary, authenticated by the bootstrapped admin.

const ADMIN_EMAIL = process.env.REKAM_ADMIN_EMAIL || 'admin@rekam.local';
const ADMIN_PASSWORD = process.env.REKAM_ADMIN_PASSWORD || 'rekam-visual-test';

// login drives the real login form (email + password) so the browser holds the
// same httpOnly session cookie a user would. It lands on the catalog (app shell).
// Defaults to the seeded admin; pass creds to sign in as someone else, which is
// how tests exercise what a non-manager actually sees.
async function login(page, baseUrl, { email = ADMIN_EMAIL, password = ADMIN_PASSWORD } = {}) {
  await page.goto(`${baseUrl}/#/login`, { waitUntil: 'networkidle' });
  await page.locator('#gate-email').waitFor({ state: 'visible', timeout: 10000 });
  await page.fill('#gate-email', email);
  await page.fill('#gate-pw', password);
  await page.click('.gate__submit');
  // The sidebar only renders once authenticated; wait for it.
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 15000 });
  await page.waitForTimeout(600);
}

// goHome navigates to the catalog root and waits for the shell.
async function goHome(page, baseUrl) {
  await page.goto(`${baseUrl}/#/`, { waitUntil: 'networkidle' });
  await page.locator('.sidebar').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(400);
}

// openRecordByTitle clicks a catalog table row whose title matches, and waits for
// the reading view. Returns true if a row was found and opened.
async function openRecordByTitle(page, title) {
  const row = page.locator('.memory-row, tr, .record-row').filter({ hasText: title }).first();
  if (await row.count() === 0) return false;
  await row.click();
  await page.locator('.editor-title, .editor-page').first().waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(500);
  return true;
}

module.exports = { login, goHome, openRecordByTitle, ADMIN_EMAIL, ADMIN_PASSWORD };
