const { test } = require('../fixtures');
const { login } = require('../helpers');

// Editing: open a record, switch to Edit, change the title, save, and confirm
// the reading view reflects the new title.
test('03-edit-and-save', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const row = page.locator('.record').filter({ hasText: 'Weekly review ritual' }).first();
  if (await row.count() === 0) throw new Error("seeded record 'Weekly review ritual' not found");
  await row.click();
  await page.locator('.editor-title').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(400);
  await snap('01-view');

  // Enter edit mode.
  await page.locator('.mode-btn', { hasText: 'Edit' }).click();
  const titleInput = page.locator('.title-input');
  await titleInput.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(600);
  await snap('02-edit-mode');

  const NEW_TITLE = 'Weekly review ritual (updated)';
  await titleInput.fill(NEW_TITLE);
  await page.waitForTimeout(300);
  await snap('03-title-changed');

  // Save.
  await page.locator('.save-row .btn-primary', { hasText: 'Save' }).click();
  const viewTitle = page.locator('.editor-title');
  await viewTitle.waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(500);
  const got = (await viewTitle.textContent())?.trim();
  log(`Title after save: ${got}`);
  if (got !== NEW_TITLE) throw new Error(`save did not persist new title (got: ${got})`);
  await snap('04-saved');
});
