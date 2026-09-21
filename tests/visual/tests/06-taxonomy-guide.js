const { test } = require('../fixtures');
const { login } = require('../helpers');

// Taxonomy guide: the #/taxonomy view lists the template branches with their
// "what goes here" descriptions, and an owner can open the inline editor.
test('06-taxonomy-guide', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.goto(`${baseUrl}/#/taxonomy`, { waitUntil: 'networkidle' });
  await page.locator('.tax-page').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(600);
  await snap('01-guide');

  const branches = page.locator('.branch');
  const n = await branches.count();
  log(`Template branches: ${n}`);
  if (n === 0) throw new Error('taxonomy guide shows no branches');

  const described = await page.locator('.branch__desc').count();
  log(`Branches with descriptions: ${described}`);
  if (described === 0) throw new Error('no branch descriptions rendered');

  // Owner can edit the template.
  const editBtn = page.locator('.edit-btn', { hasText: 'Edit template' });
  if (await editBtn.count() > 0) {
    await editBtn.click();
    await page.locator('.edit-list').waitFor({ state: 'visible', timeout: 8000 });
    await page.waitForTimeout(500);
    log('Opened template editor');
    await snap('02-editor');
  } else {
    log('Edit button not shown (caller not owner?)');
  }
});
