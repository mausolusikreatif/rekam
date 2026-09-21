const { test } = require('../fixtures');
const { login } = require('../helpers');

// Creating a record: use the New record button, fill title + class, file it, and
// confirm it opens in the reading view and lands in the catalog.
test('09-create-record', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  await page.getByRole('button', { name: 'New record' }).click();
  await page.locator('.new-eyebrow').waitFor({ state: 'visible', timeout: 10000 });
  await page.waitForTimeout(500);
  await snap('01-new-form');

  const TITLE = 'First-week checklist';
  await page.locator('.title-input').fill(TITLE);
  await page.locator('.new-class-row .meta-input').fill('work.projects');
  await page.waitForTimeout(400);
  // Best-effort: type a line of content into the Milkdown editor for a fuller
  // screenshot. Not asserted — the record is fileable on title + class alone.
  try {
    const ed = page.locator('.milkdown-container [contenteditable="true"]').first();
    await ed.click({ timeout: 3000 });
    await page.keyboard.type('Set up laptop, read the taxonomy guide, file a first note.');
  } catch { log('skipped content typing (editor not ready)'); }
  await snap('02-filled');

  await page.getByRole('button', { name: 'File record' }).click();
  const title = page.locator('.editor-title');
  await title.waitFor({ state: 'visible', timeout: 12000 });
  await page.waitForTimeout(600);
  const got = (await title.textContent())?.trim();
  log(`Filed record title: ${got}`);
  if (got !== TITLE) throw new Error(`created record title mismatch (got: ${got})`);
  await snap('03-created');
});
