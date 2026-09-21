const { test } = require('../fixtures');
const { login } = require('../helpers');

// Team settings from the other side of the permission line. Bob is an editor in
// Acme Research: he may see who he works with and what the team is called, and
// may change neither. 07 covers the owner's view, so this is the half where the
// controls have to be absent rather than present.
test('19-team-settings-as-member', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl, { email: 'bob@rekam.local', password: 'password123' });

  const teamSwitcher = page.locator('.switcher', { has: page.locator('[title="Switch workspace"]') });
  const trigger = teamSwitcher.locator('.current');

  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(300);
  await snap('01-member-switcher');

  const row = teamSwitcher.locator('.team-row').filter({ hasText: 'Acme Research' }).first();
  if (await row.count() === 0) throw new Error('an invited editor should see the team in their switcher');
  await row.click();
  await page.waitForTimeout(1200);

  // Team settings is offered to every member, not just managers.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  const settings = teamSwitcher.locator('.menu__action', { hasText: 'Team settings' });
  if (await settings.count() === 0) throw new Error('a member should be able to open team settings');
  await settings.click();

  const modal = page.locator('.modal[aria-label="Team settings"]');
  await modal.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(500);

  // General: the name is visible but not editable, and there is no way to delete
  // a team you do not own.
  if (!(await modal.locator('#team-name').isDisabled())) {
    throw new Error('an editor must not be able to rename the team');
  }
  if (await modal.locator('.danger').count() !== 0) {
    throw new Error('the danger zone must not be shown to a non-owner');
  }
  log('General: name read-only, no danger zone');
  await snap('02-general-read-only');

  // Members: the roster reads, but roles are static text and there is no invite
  // form or remove button.
  await modal.locator('.tab', { hasText: 'Members' }).click();
  await page.waitForTimeout(600);
  const members = await modal.locator('.member').count();
  if (members < 2) throw new Error(`expected the full roster, got ${members}`);
  if (await modal.locator('select.role').count() !== 0) {
    throw new Error('an editor must not get role dropdowns');
  }
  if (await modal.locator('.remove').count() !== 0) {
    throw new Error('an editor must not get remove buttons');
  }
  if (await modal.locator('.invite').count() !== 0) {
    throw new Error('an editor must not get the invite form');
  }
  const roles = await modal.locator('.role-static').allInnerTexts();
  log(`Roster (read-only): ${members} members, roles ${roles.map((r) => r.trim()).join(', ')}`);
  await snap('03-members-read-only');
});
