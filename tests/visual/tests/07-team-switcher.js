const { test } = require('../fixtures');
const { login } = require('../helpers');

// Teams: the switcher lists Personal + the seeded 'Acme Research'. Switching into
// it and opening Team settings shows the General panel (rename + owner danger
// zone) and the Members roster, which must name people rather than print uuids.
test('07-team-switcher', async ({ page, baseUrl, snap, log }) => {
  await login(page, baseUrl);

  // The team switcher and the identity switcher both use .switcher/.current, so
  // scope to the team one by its unique trigger title.
  const teamSwitcher = page.locator('.switcher', { has: page.locator('[title="Switch workspace"]') });
  const trigger = teamSwitcher.locator('.current');

  // Open the workspace switcher.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(400);
  await snap('01-switcher-open');

  const teamRow = teamSwitcher.locator('.team-row').filter({ hasText: 'Acme Research' }).first();
  if (await teamRow.count() === 0) throw new Error("seeded team 'Acme Research' not in switcher");
  log('Team Acme Research present');
  await teamRow.click();
  await page.waitForTimeout(1200); // switch resets view + reloads
  await snap('02-switched-to-team');

  // Reopen and open Team settings.
  await trigger.click();
  await teamSwitcher.locator('.menu').waitFor({ state: 'visible', timeout: 8000 });
  const settings = teamSwitcher.locator('.menu__action', { hasText: 'Team settings' });
  if (await settings.count() === 0) throw new Error('Team settings action not available for owner');
  await settings.click();

  const modal = page.locator('.modal[aria-label="Team settings"]');
  await modal.waitFor({ state: 'visible', timeout: 8000 });
  await page.waitForTimeout(500);
  // General: rename field prefilled, and the owner-only danger zone present.
  const nameValue = await modal.locator('#team-name').inputValue();
  if (nameValue !== 'Acme Research') throw new Error(`rename field = ${nameValue}, want 'Acme Research'`);
  if (await modal.locator('.danger').count() === 0) throw new Error('owner should see the danger zone');
  await snap('03-settings-general');

  // Danger zone expanded: the confirm-by-name step, not the delete itself — the
  // seeded team has to survive for the rest of the suite.
  await modal.locator('.btn-danger', { hasText: 'Delete this team' }).click();
  await page.waitForTimeout(300);
  await snap('04-danger-zone');
  await modal.locator('.btn-sm', { hasText: 'Cancel' }).click();

  // Members: the roster names people. A row showing a bare uuid is the bug this
  // panel was rebuilt to fix, so assert on the name, not just the row count.
  await modal.locator('.tab', { hasText: 'Members' }).click();
  await page.waitForTimeout(600);
  const members = await modal.locator('.member').count();
  log(`Team members: ${members}`);
  if (members < 2) throw new Error(`expected >=2 members (owner + invited), got ${members}`);
  const names = await modal.locator('.member__name').allInnerTexts();
  log(`Roster: ${names.map((n) => n.replace(/\s+/g, ' ').trim()).join(' | ')}`);
  if (!names.some((n) => n.includes('Bob Rivera'))) {
    throw new Error(`roster should name the invited editor, got: ${JSON.stringify(names)}`);
  }
  await snap('05-members-panel');
});
