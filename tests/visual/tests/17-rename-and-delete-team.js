const { expect } = require('@playwright/test');
const { test } = require('../fixtures');
const { login } = require('../helpers');

// Team settings, end to end, on the throwaway team 16 created: rename it, then
// delete it. Deletion is the one destructive action in the UI, so this covers the
// whole path — confirm-by-name, the team leaving the switcher, and the fallback
// to Personal — rather than stopping at the confirm step the way 07 does.
test('17-rename-and-delete-team', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  const teamSwitcher = page.locator('.switcher', { has: page.locator('[title="Switch workspace"]') });
  const trigger = teamSwitcher.locator('.current');

  const CREATED = 'Field Notes QA';
  const RENAMED = 'Field Notes (retired)';

  // Switch into the team 16 left behind.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  const row = teamSwitcher.locator('.team-row').filter({ hasText: CREATED }).first();
  if (await row.count() === 0) throw new Error(`'${CREATED}' not in switcher; 16-create-team must run first`);
  await row.click();
  await page.waitForTimeout(1200);

  // Open settings and rename.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  await teamSwitcher.locator('.menu__action', { hasText: 'Team settings' }).click();
  const modal = page.locator('.modal[aria-label="Team settings"]');
  await modal.waitFor({ state: 'visible', timeout: 8000 });

  await modal.locator('#team-name').fill(RENAMED);
  await page.waitForTimeout(200);
  await modal.locator('.btn-primary', { hasText: 'Rename' }).click();
  await expect(modal.locator('.modal__title')).toHaveText(RENAMED, { timeout: 10000 });
  log(`Title after rename: ${RENAMED}`);
  await page.waitForTimeout(400);
  await snap('01-renamed');

  // Delete: the button stays disabled until the typed name matches exactly.
  await modal.locator('.btn-danger', { hasText: 'Delete this team' }).click();
  await page.waitForTimeout(300);
  const confirmBtn = modal.locator('.btn-danger', { hasText: 'Delete team' });
  if (!(await confirmBtn.isDisabled())) throw new Error('delete must be disabled before the name is typed');
  await modal.locator('.confirm').fill('not the name');
  await page.waitForTimeout(200);
  if (!(await confirmBtn.isDisabled())) throw new Error('delete must stay disabled for a wrong name');
  await modal.locator('.confirm').fill(RENAMED);
  await page.waitForTimeout(300);
  await snap('02-confirm-armed');

  await confirmBtn.click();

  // The modal closes and the workspace falls back to Personal.
  await expect(modal).toHaveCount(0, { timeout: 10000 });
  await expect(teamSwitcher.locator('.current__name')).toHaveText('Personal', { timeout: 10000 });
  log('Active workspace after delete: Personal');

  // And the team is gone from the switcher for good.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(400);
  const stillThere = await teamSwitcher.locator('.team-row').filter({ hasText: 'Field Notes' }).count();
  if (stillThere > 0) throw new Error('deleted team still listed in the switcher');
  await snap('03-gone-from-switcher');
});
